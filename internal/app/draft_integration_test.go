package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func draftFixture(t *testing.T) (*App, *fixture.Browser) {
	t.Helper()
	var connected atomic.Bool
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if connected.Load() {
			t.Error("Draft or Preview contacted Telegram")
			w.WriteHeader(500)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Draft Bot","username":"draft_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		default:
			t.Error("Draft or Preview changed Telegram delivery")
			w.WriteHeader(500)
		}
	})
	b.Send(http.MethodGet, "/bots/connect", nil)
	if got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	connected.Store(true)
	return a, b
}

func TestPreviewRequiresSavedDraftAndExpiresWithoutChangingDraft(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.Post("/bots/1/preview", url.Values{}); got.Code != 409 {
		t.Fatalf("unsaved Preview: %d", got.Code)
	}
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	// Advance time at the storage boundary; assertions use the HTTP contract.
	if _, err := a.db.Exec("UPDATE bot_previews SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if got := b.Send(http.MethodGet, path, nil); got.Code != 404 {
		t.Fatalf("expired Preview: %d", got.Code)
	}
	if got := b.Post(path+"/restart", url.Values{"revision": {"1"}}); got.Code != 404 {
		t.Fatalf("expired restart: %d", got.Code)
	}
	if err := a.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.DraftText(t, 1), "Call 02112345678") {
		t.Fatal("expiration removed Draft")
	}
}
