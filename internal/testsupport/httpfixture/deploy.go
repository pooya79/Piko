package httpfixture

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/testsupport"
)

// Keep all observations at HTTP HTTP and the external Telegram/provider seams.
func DeployFixture(t *testing.T, provider http.HandlerFunc) (*HTTP, *Browser, *TelegramFake) {
	t.Helper()
	a, b, f := GeneralDeployFixture(t, provider)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات انتشار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"گفتگوی اول", "گفتگوی دوم"} {
		if got := b.Post("/bots/1/chats", url.Values{"title": {title}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	if got := b.Post("/bots/1/connect", url.Values{"token": {TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, f
}

func GeneralDeployFixture(t *testing.T, provider http.HandlerFunc) (*HTTP, *Browser, *TelegramFake) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	f := &TelegramFake{}
	api := httptest.NewServer(f)
	t.Cleanup(api.Close)
	cfg := httpapp.Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "deploy-test-session-secret-at-least-32", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", BotPublicURL: "https://piko.example.test", LogLevel: "error", ShutdownPeriod: time.Second}
	if provider != nil {
		model := httptest.NewServer(StreamingBuilderProvider(provider))
		t.Cleanup(model.Close)
		cfg.Builder = builder.Config{APIKey: "test-server-key", BaseURL: model.URL + "/v1"}
	}
	a, err := NewWithTelegram(t.Context(), cfg, telegram.NewClient(api.URL, api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.StopWork(); a.Builder.Wait(); _ = a.DB.Close() })
	b := NewAccountBrowser(t, a.Handler)
	b.Send("GET", "/register", nil)
	if got := b.Post("/register", RegisterValues("deploy-owner@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, f
}
