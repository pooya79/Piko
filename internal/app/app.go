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

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
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
	composed, err := httpapp.New(ctx, cfg, api, now)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: composed.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	a := &App{cfg: cfg, log: composed.Log, db: composed.DB, server: server, bots: composed.Bots, builder: composed.Builder, now: now, requestContext: requestCtx, cancelRequests: cancelRequests}
	server.Handler = a.trackRequests(composed.Handler)
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
