// Package httpapp composes the application's HTTP handlers and their concrete
// dependencies independently of the server's background work and shutdown.
package httpapp

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/platform/logging"
	webx "github.com/pooya79/Piko/internal/web"
)

// HTTP owns the composed dependencies. The caller must stop and join Builder
// work before closing DB; the server lifecycle remains in package app.
type HTTP struct {
	DB      *sql.DB
	Handler http.Handler
	Bots    *bot.Service
	Builder *builder.Service
	Log     *slog.Logger
}

// New requires an already migrated SQLite file. It starts no HTTP listener,
// cleanup loop, or Telegram delivery worker.
func New(ctx context.Context, cfg Config, api *telegram.Client, now func() time.Time) (*HTTP, error) {
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
	return &HTTP{DB: db, Handler: Router(db, mw, limiter, authHandler, botService, builderService), Bots: botService, Builder: builderService, Log: log}, nil
}
