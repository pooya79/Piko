package app

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/testsupport"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func testBotService(t *testing.T, db *sql.DB) *bot.Service {
	t.Helper()
	s, err := bot.NewService(bot.NewRepository(db), telegram.NewClient("https://api.telegram.org", http.DefaultClient), []byte("0123456789abcdef0123456789abcdef"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func botFixture(t *testing.T, api http.HandlerFunc) (*App, *fixture.Browser, string) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := httptest.NewServer(api)
	t.Cleanup(fake.Close)
	cfg := Config{DatabasePath: path, SessionSecret: "bot-test-session-secret-with-32-characters", BotEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), LogLevel: "error"}
	a, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(fake.URL, fake.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	b := fixture.NewAccountBrowser(t, a.server.Handler)
	b.Send(http.MethodGet, "/register", nil)
	if got := b.Post("/register", fixture.RegisterValues("bot-owner@example.test", "مینا", "OwnerPassword123")); got.Code != http.StatusSeeOther {
		t.Fatalf("register: %d", got.Code)
	}
	return a, b, path
}
