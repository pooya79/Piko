package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"buildx/internal/auth"
	"buildx/internal/jobs"
	"buildx/internal/locale"
	"buildx/internal/platform/cache"
	"buildx/internal/platform/database"
	"buildx/internal/platform/database/dbgen"
	"buildx/internal/platform/logging"
	webx "buildx/internal/web"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	loginRateLimit          = 10
	loginRateWindow         = time.Minute
	accountEmailRateLimit   = 5
	accountEmailRateWindow  = time.Hour
	accountActionRateLimit  = 10
	accountActionRateWindow = time.Minute
)

type App struct {
	cfg    Config
	log    *slog.Logger
	db     *pgxpool.Pool
	redis  *redis.Client
	server *http.Server
}

func New(ctx context.Context, cfg Config) (*App, error) {
	log := logging.New(cfg.LogLevel)
	catalog, e := locale.NewCatalog()
	if e != nil {
		return nil, e
	}
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		return nil, e
	}
	redisClient, e := cache.Open(ctx, cfg.RedisURL)
	if e != nil {
		db.Close()
		return nil, e
	}
	q := dbgen.New(db)
	authService := auth.NewService(q)
	mailQueue, e := jobs.NewEmailEnqueuer(db, log)
	if e != nil {
		_ = redisClient.Close()
		db.Close()
		return nil, e
	}
	accountService := auth.NewAccountService(auth.NewAccountRepository(db, mailQueue), authService, []byte(cfg.SessionSecret), cfg.PublicBaseURL, catalog)
	authHandler := auth.NewHandler(authService, accountService, log, cfg.CookieSecure)
	mw := webx.Middleware{LocaleCatalog: catalog, Auth: authService, Log: log, SecureCookie: cfg.CookieSecure, TrustedProxy: cfg.TrustedProxy, Secret: []byte(cfg.SessionSecret)}
	limiter := webx.NewRateLimiter(redisClient, log, mw.ClientIP)
	router := buildRouter(db, mw, limiter, authHandler)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	return &App{cfg: cfg, log: log, db: db, redis: redisClient, server: server}, nil
}
func (a *App) Run(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		a.log.Info("server starting", "addr", a.cfg.HTTPAddr, "environment", a.cfg.Environment)
		serverErr <- a.server.ListenAndServe()
	}()
	var runErr error
	select {
	case <-ctx.Done():
	case e := <-serverErr:
		if !errors.Is(e, http.ErrServerClosed) {
			runErr = e
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownPeriod)
	defer cancel()
	a.log.Info("server shutting down")
	if e := a.server.Shutdown(shutdownCtx); e != nil && runErr == nil {
		runErr = fmt.Errorf("http shutdown: %w", e)
	}
	if e := a.redis.Close(); e != nil {
		a.log.Warn("close redis", "error", e)
	}
	a.db.Close()
	return runErr
}

// buildRouter loads sessions before CSRF checks so the latter can choose session-bound tokens.
func buildRouter(db *pgxpool.Pool, mw webx.Middleware, limiter *webx.RateLimiter, ah *auth.Handler) http.Handler {
	r := chi.NewRouter()
	// The inner recovery sees the account locale; the outer one also covers
	// failures while loading the request locale or session.
	r.Use(mw.RequestID, mw.Recover, mw.Logging, mw.Security, mw.LimitBody, mw.RequestLocale, mw.Session, mw.Recover, mw.CSRF)
	// Only unmatched routes use this presentation; registered health and static
	// handlers retain their machine-facing responses and failure semantics.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		webx.RenderError(w, r, http.StatusNotFound, "Page not found.")
	})
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if e := db.Ping(ctx); e != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := auth.UserFromContext(r.Context()); ok {
			if u.Verified {
				http.Redirect(w, r, "/account", http.StatusFound)
			} else {
				http.Redirect(w, r, "/verify/pending", http.StatusFound)
			}
		} else {
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	})
	r.Group(func(r chi.Router) {
		r.With(limiter.Middleware("login", loginRateLimit, loginRateWindow)).Post("/login", ah.Login)
		r.Get("/login", ah.LoginForm)
		r.Post("/language", (locale.Handler{SecureCookie: mw.SecureCookie, SaveAccountLanguage: ah.SaveLanguage, ShowError: webx.RenderError}).Switch)
		// All mail-triggering routes share one strict IP budget.
		r.With(limiter.MiddlewareStrict("account-email", accountEmailRateLimit, accountEmailRateWindow)).Post("/register", ah.Register)
		r.Get("/register", ah.RegisterForm)
		r.Get("/verify/pending", ah.Pending)
		r.Get("/verify/resend", ah.ResendForm)
		r.Get("/verify", ah.VerifyForm)
		r.With(limiter.Middleware("account-action", accountActionRateLimit, accountActionRateWindow)).Post("/verify", ah.Verify)
		r.Get("/verify/cancel", ah.CancelForm)
		r.Post("/verify/cancel", ah.Cancel)
		r.With(limiter.MiddlewareStrict("account-email", accountEmailRateLimit, accountEmailRateWindow)).Post("/verify/resend", ah.ResendVerification)
		r.Get("/password/forgot", ah.ForgotForm)
		r.With(limiter.MiddlewareStrict("account-email", accountEmailRateLimit, accountEmailRateWindow)).Post("/password/forgot", ah.Forgot)
		r.Get("/password/reset", ah.ResetForm)
		r.With(limiter.Middleware("account-action", accountActionRateLimit, accountActionRateWindow)).Post("/password/reset", ah.Reset)
		r.Post("/logout", ah.Logout)
	})
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)
		r.Get("/account", ah.Profile)
	})
	return r
}
