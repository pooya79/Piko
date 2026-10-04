package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
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
	cfg            Config
	log            *slog.Logger
	db             *sql.DB
	server         *http.Server
	bots           *bot.Service
	builder        *builder.Service
	now            func() time.Time
	requestMu      sync.Mutex
	requests       sync.WaitGroup
	stopping       bool
	requestContext context.Context
	cancelRequests context.CancelFunc
}

func New(ctx context.Context, cfg Config) (*App, error) {
	return newWithTelegram(ctx, cfg, telegram.NewClient("https://api.telegram.org", http.DefaultClient))
}

func newWithTelegram(ctx context.Context, cfg Config, api *telegram.Client) (*App, error) {
	return newWithTelegramClock(ctx, cfg, api, time.Now)
}

func newWithTelegramClock(ctx context.Context, cfg Config, api *telegram.Client, now func() time.Time) (*App, error) {
	key, err := bot.EncryptionKey(cfg.BotEncryptionKey, cfg.SessionSecret)
	if err != nil {
		return nil, err
	}
	log := logging.New(cfg.LogLevel)
	if err := cfg.Builder.Validate(); err != nil {
		return nil, err
	}
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
	botService, e := bot.NewService(bot.NewRepository(db), api, key, now)
	if e != nil {
		_ = db.Close()
		return nil, e
	}
	if err := botService.ConfigureDelivery(cfg.Environment, cfg.BotPublicURL); err != nil {
		_ = db.Close()
		return nil, err
	}
	authService := auth.NewService(q)
	accountService := auth.NewAccountService(auth.NewAccountRepository(db), authService)
	authHandler := auth.NewHandler(authService, accountService, log, cfg.CookieSecure)
	mw := webx.Middleware{LocaleCatalog: catalog, Auth: authService, Log: log, SecureCookie: cfg.CookieSecure, TrustedProxy: cfg.TrustedProxy, Secret: []byte(cfg.SessionSecret)}
	limiter := webx.NewRateLimiter(db, log, mw.ClientIP)
	builderService := builder.NewService(builder.NewRepository(db), botService)
	if err := builderService.Configure(cfg.Builder, now); err != nil {
		_ = db.Close()
		return nil, err
	}
	builderService.ConfigureTracing(log, cfg.SessionSecret, cfg.BotEncryptionKey)
	router := buildRouter(db, mw, limiter, authHandler, botService, builderService)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	a := &App{cfg: cfg, log: log, db: db, server: server, bots: botService, builder: builderService, now: now, requestContext: requestCtx, cancelRequests: cancelRequests}
	server.Handler = a.trackRequests(router)
	return a, nil
}
func (a *App) Run(ctx context.Context) error {
	defer func() { _ = a.db.Close() }()
	defer func() { a.builder.Stop(a.cfg.ShutdownPeriod); a.builder.Wait() }()
	defer func() { a.stopRequests(); a.requests.Wait() }()
	if err := a.cleanup(ctx); err != nil {
		return fmt.Errorf("startup cleanup: %w", err)
	}
	if err := a.builder.Recover(ctx); err != nil {
		return fmt.Errorf("recover Builder runs: %w", err)
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
	deliveryCtx, stopDelivery := context.WithCancel(ctx)
	deliveryDone := make(chan struct{})
	go func() { defer close(deliveryDone); a.bots.RunDelivery(deliveryCtx) }()
	defer func() { stopDelivery(); <-deliveryDone }()
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
	a.stopRequests()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownPeriod)
	defer cancel()
	a.log.Info("server shutting down")
	if e := a.server.Shutdown(shutdownCtx); e != nil && runErr == nil {
		runErr = fmt.Errorf("http shutdown: %w", e)
	}
	if shutdownCtx.Err() != nil {
		_ = a.server.Close()
	}
	a.requests.Wait()
	return runErr
}

// buildRouter loads sessions before CSRF checks so the latter can choose session-bound tokens.
func buildRouter(db *sql.DB, mw webx.Middleware, limiter *webx.RateLimiter, ah *auth.Handler, bots *bot.Service, builders *builder.Service) http.Handler {
	bh := bot.NewHandler(bots, mw.Log)
	if builders == nil {
		builders = builder.NewService(builder.NewRepository(db), bots)
	}
	builderHandler := builder.NewHandler(builders, bots, mw.Log)
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
		r.Get("/builder", builderHandler.Index)
		r.Get("/bots/{botID}/chats", builderHandler.List)
		r.Post("/bots/{botID}/chats", builderHandler.Create)
		r.Get("/bots/{botID}/chats/{chatID}", builderHandler.Detail)
		r.Get("/bots/{botID}/chats/{chatID}/status", builderHandler.Status)
		r.Get("/bots/{botID}/chats/{chatID}/stream", builderHandler.Stream)
		r.Post("/bots/{botID}/chats/{chatID}/messages", builderHandler.Send)
		r.Post("/bots/{botID}/chats/{chatID}/runs/{runID}/retry", builderHandler.Retry)
		r.Post("/bots/{botID}/chats/{chatID}/runs/{runID}/stop", builderHandler.Stop)
		r.Post("/bots/{botID}/chats/{chatID}/runs/{runID}/undo", builderHandler.Undo)
		r.Post("/bots/{botID}/chats/{chatID}/delete", builderHandler.Delete)
		r.Get("/bots", bh.List)
		r.Get("/bots/new", bh.CreateForm)
		r.With(limiter.MiddlewareStrict("bot-create", 10, time.Minute)).Post("/bots/new", bh.Create)
		r.Get("/bots/connect", bh.ConnectForm)
		r.With(limiter.MiddlewareStrict("bot-connect", 10, time.Minute)).Post("/bots/connect", bh.Connect)
		r.Get("/bots/{botID}", bh.Detail)
		r.Get("/bots/{botID}/connect", bh.ConnectExistingForm)
		r.With(limiter.MiddlewareStrict("bot-connect", 10, time.Minute)).Post("/bots/{botID}/connect", bh.ConnectExisting)
		r.With(limiter.MiddlewareStrict("bot-credentials", 10, time.Minute)).Post("/bots/{botID}/replace-token", bh.ReplaceToken)
		r.With(limiter.MiddlewareStrict("bot-credentials", 10, time.Minute)).Post("/bots/{botID}/reconnect", bh.Reconnect)
		r.Post("/bots/{botID}/disconnect", bh.Disconnect)
		r.Post("/bots/{botID}/delete", bh.DeleteBot)
		r.Get("/bots/{botID}/submissions", bh.Submissions)
		r.Get("/bots/{botID}/submissions/{submissionID}", bh.Submission)
		r.Post("/bots/{botID}/submissions/{submissionID}/delete", bh.DeleteSubmission)
		r.Get("/bots/{botID}/activate", bh.Activation)
		r.With(limiter.MiddlewareStrict("bot-activate", 10, time.Minute)).Post("/bots/{botID}/activate", bh.Activate)
		r.Post("/bots/{botID}/publish", bh.Publish)
		r.With(limiter.MiddlewareStrict("bot-activate", 10, time.Minute)).Post("/bots/{botID}/deploy", bh.Deploy)
		r.Post("/bots/{botID}/pause", bh.Pause)
		r.Post("/bots/{botID}/resume", bh.Resume)
		r.Get("/bots/{botID}/draft", bh.Draft)
		r.Post("/bots/{botID}/draft", bh.SaveDraft)
		r.Get("/bots/{botID}/preview", bh.PreviewLanding)
		r.With(limiter.MiddlewareStrict("bot-preview", 20, time.Minute)).Post("/bots/{botID}/preview", bh.StartPreview)
		r.Get("/bots/{botID}/preview/{previewID}", bh.Preview)
		r.Post("/bots/{botID}/preview/{previewID}/choose", bh.ChoosePreview)
		r.Post("/bots/{botID}/preview/{previewID}/restart", bh.RestartPreview)
	})
	ingress := chi.NewRouter()
	ingress.Use(mw.RequestID, mw.Logging, mw.Security)
	ingress.Post("/telegram/bots/{botID}", bh.Webhook)
	ingress.Mount("/", r)
	return ingress
}
