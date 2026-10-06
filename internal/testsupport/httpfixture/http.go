// Package httpfixture supplies real HTTP/SQLite dependencies for feature tests.
// It starts no HTTP listener or background delivery/cleanup workers. Tests of
// server startup, request draining, and shutdown belong in internal/app.
package httpfixture

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
)

// HTTP is a fixture, with storage exposed for arranging transaction errors
// and legacy schemas. It does not emulate the server's lifecycle.
type HTTP struct {
	DB      *sql.DB
	Handler http.Handler
	Config  httpapp.Config
	Bots    *bot.Service
	Builder *builder.Service
	Now     func() time.Time
}

func New(ctx context.Context, cfg httpapp.Config) (*HTTP, error) {
	return NewWithTelegram(ctx, cfg, telegram.NewClient("https://api.telegram.org", http.DefaultClient))
}

func NewWithTelegram(ctx context.Context, cfg httpapp.Config, api *telegram.Client) (*HTTP, error) {
	return NewWithTelegramClock(ctx, cfg, api, time.Now)
}

func NewWithTelegramClock(ctx context.Context, cfg httpapp.Config, api *telegram.Client, now func() time.Time) (*HTTP, error) {
	composed, err := httpapp.New(ctx, cfg, api, now)
	if err != nil {
		return nil, err
	}
	return &HTTP{DB: composed.DB, Handler: composed.Handler, Config: cfg, Bots: composed.Bots, Builder: composed.Builder, Now: now}, nil
}

// StopWork cancels Builder work; callers join it before closing storage.
func (a *HTTP) StopWork() {
	a.Builder.Stop(a.Config.ShutdownPeriod)
}
