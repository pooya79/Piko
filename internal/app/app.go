package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/dashboard"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/platform/logging"
	webx "github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/shell"
)

const (
	loginRateLimit         = 10
	loginRateWindow        = time.Minute
	registrationRateLimit  = 5
	registrationRateWindow = time.Hour
)

type App struct {
	cfg    Config
	log    *slog.Logger
	db     *sql.DB
	server *http.Server
}

func New(ctx context.Context, cfg Config) (*App, error) {
	return newWithTelegram(ctx, cfg, telegram.NewClient("https://api.telegram.org", http.DefaultClient))
}

func newWithTelegram(ctx context.Context, cfg Config, api *telegram.Client) (*App, error) {
	key, err := bot.EncryptionKey(cfg.BotEncryptionKey, cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	log := logging.New(cfg.LogLevel)
	catalog, e := locale.NewCatalog()
	if e != nil {
		return nil, e
	}
	db, e := database.Open(ctx, cfg.DatabasePath)
	if e != nil {
		return nil, e
	}
	if e := database.RequireWAL(ctx, db); e != nil {
		_ = db.Close()
		return nil, e
	}
	q := dbgen.New(db)
	botService, e := bot.NewService(bot.NewRepository(q), api, key)
	if e != nil {
		_ = db.Close()
		return nil, e
	}
	authService := auth.NewService(q)
	accountService := auth.NewAccountService(auth.NewAccountRepository(db), authService)
	authHandler := auth.NewHandler(authService, accountService, log, cfg.CookieSecure)
	mw := webx.Middleware{LocaleCatalog: catalog, Auth: authService, Log: log, SecureCookie: cfg.CookieSecure, TrustedProxy: cfg.TrustedProxy, Secret: []byte(cfg.SessionSecret)}
	limiter := webx.NewRateLimiter(db, log, mw.ClientIP)
	router := buildRouter(db, mw, limiter, authHandler, botService)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	return &App{cfg: cfg, log: log, db: db, server: server}, nil
}
func (a *App) Run(ctx context.Context) error {
	defer func() { _ = a.db.Close() }()
	if err := a.cleanup(ctx); err != nil {
		return fmt.Errorf("startup cleanup: %w", err)
	}
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	ticker := time.NewTicker(time.Hour)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		a.cleanupLoop(cleanupCtx, ticker.C)
	}()
	// Registered after database close's defer, so cleanup always stops first.
	defer func() {
		stopCleanup()
		ticker.Stop()
		<-cleanupDone
	}()
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
	return runErr
}

// buildRouter loads sessions before CSRF checks so the latter can choose session-bound tokens.
func buildRouter(db *sql.DB, mw webx.Middleware, limiter *webx.RateLimiter, ah *auth.Handler, bots *bot.Service) http.Handler {
	bh := bot.NewHandler(bots, mw.Log)
	r := chi.NewRouter()
	// The inner recovery sees the account locale; the outer one also covers
	// failures while loading the request locale or session.
	r.Use(mw.RequestID, mw.Recover, mw.Logging, mw.Security, mw.LimitBody, mw.RequestLocale, mw.Session, mw.Recover, mw.CSRF)
	// Only unmatched routes use this presentation; registered health and static
	// handlers retain their machine-facing responses and failure semantics.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		webx.RenderError(w, r, http.StatusNotFound, "error.message.page.missing")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		// A custom chi responder replaces its default Allow-header handling.
		for _, method := range []string{http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace} {
			if r.Match(chi.NewRouteContext(), method, req.URL.Path) {
				w.Header().Add("Allow", method)
			}
		}
		webx.RenderError(w, req, http.StatusMethodNotAllowed, "error.message.method")
	})
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if e := db.PingContext(ctx); e != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.UserFromContext(r.Context()); ok {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
		} else {
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	})
	r.Group(func(r chi.Router) {
		r.With(limiter.Middleware("login", loginRateLimit, loginRateWindow)).Post("/login", ah.Login)
		r.Get("/login", ah.LoginForm)
		r.With(limiter.MiddlewareStrict("register", registrationRateLimit, registrationRateWindow)).Post("/register", ah.Register)
		r.Get("/register", ah.RegisterForm)
		r.Post("/logout", ah.Logout)
	})
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				owned, err := bots.List(req.Context())
				if err != nil {
					webx.RenderError(w, req, 500, "error.message.server")
					return
				}
				next.ServeHTTP(w, req.WithContext(shell.WithNavigation(req.Context(), bot.Navigation(owned))))
			})
		})
		r.Get("/account", ah.Profile)
		r.Get("/dashboard", dashboard.NewHandler(bots))
		r.Get("/bots", bh.List)
		r.Get("/bots/connect", bh.ConnectForm)
		r.With(limiter.MiddlewareStrict("bot-connect", 10, time.Minute)).Post("/bots/connect", bh.Connect)
		r.Get("/bots/{botID}", bh.Detail)
		r.Get("/bots/{botID}/draft", bh.Draft)
		r.Post("/bots/{botID}/draft", bh.SaveDraft)
		r.Get("/bots/{botID}/preview", bh.PreviewLanding)
		r.With(limiter.MiddlewareStrict("bot-preview", 20, time.Minute)).Post("/bots/{botID}/preview", bh.StartPreview)
		r.Get("/bots/{botID}/preview/{previewID}", bh.Preview)
		r.Post("/bots/{botID}/preview/{previewID}/choose", bh.ChoosePreview)
		r.Post("/bots/{botID}/preview/{previewID}/restart", bh.RestartPreview)
	})
	return r
}
