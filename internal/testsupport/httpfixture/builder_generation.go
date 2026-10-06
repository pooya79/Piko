package httpfixture

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/testsupport"
)

func BuilderFixture(t *testing.T, config builder.Config, provider http.HandlerFunc) (*HTTP, *Browser) {
	t.Helper()
	return BuilderFixtureWithLogLevel(t, config, "error", provider)
}

func BuilderFixtureWithLogLevel(t *testing.T, config builder.Config, logLevel string, provider http.HandlerFunc) (*HTTP, *Browser) {
	t.Helper()
	a, b := GeneralBuilderFixture(t, config, logLevel, provider)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات گفتگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"گفتگوی اول", "گفتگوی دوم"} {
		SeedBotChat(t, a.DB, 1, title)
	}
	return a, b
}

func GeneralBuilderFixture(t *testing.T, config builder.Config, logLevel string, provider http.HandlerFunc) (*HTTP, *Browser) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := httptest.NewServer(StreamingBuilderProvider(provider))
	t.Cleanup(fake.Close)
	config.APIKey, config.BaseURL = "test-server-key", fake.URL+"/v1"
	cfg := httpapp.Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "builder-test-secret-at-least-32-characters", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", LogLevel: logLevel, ShutdownPeriod: time.Second, Builder: config}
	a, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.StopWork(); a.Builder.Wait(); _ = a.DB.Close() })
	b := NewAccountBrowser(t, a.Handler)
	b.Send("GET", "/register", nil)
	if got := b.Post("/register", RegisterValues("builder-owner@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b
}

func WaitBuilder(t *testing.T, b *Browser, path, status string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page := b.Send("GET", path, nil)
		if page.Code != 200 {
			t.Fatal(page.Code)
		}
		body := page.Body.String()
		last := strings.LastIndex(body, `data-run-status=`)
		if last >= 0 && strings.HasPrefix(body[last:], `data-run-status="`+status+`"`) {
			return page.Body.String()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Builder outcome did not become " + status)
	return ""
}
