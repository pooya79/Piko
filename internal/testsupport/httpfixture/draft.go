package httpfixture

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const StructuredDraft = `{"version":1,"welcome":{"id":"welcome","type":"message","text":"Hello"},"menu":{"id":"menu","type":"menu","text":"Choose","choices":[{"id":"hours","label":"Hours","target":"reply"}]},"messages":[{"id":"reply","type":"message","text":"Open 9 to 5"}]}`

func DraftFixture(t *testing.T) (*HTTP, *Browser) {
	t.Helper()
	var connected atomic.Bool
	a, b, _ := BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
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
	if got := b.Post("/bots/connect", url.Values{"token": {TestBotToken}}); got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	connected.Store(true)
	return a, b
}

// postDraft models a manual editor loaded before submitting a candidate.
// Explicit revisions preserve older editors for conflict and validation journeys.
func (b *Browser) PostDraft(t *testing.T, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if _, supplied := values["draft_revision"]; !supplied {
		page := b.Send(http.MethodGet, path, nil)
		if page.Code == http.StatusOK {
			values.Set("draft_revision", RenderedDraft(t, page.Body.String()).Get("draft_revision"))
		}
	}
	return b.Post(path, values)
}

func WelcomeDraft() url.Values {
	return url.Values{"welcome": {"سلام <دوست>"}, "menu_prompt": {"چه چیزی می\u200cخواهی؟"}, "choice_label": {"ساعت کار", "Contact"}, "choice_message": {"شنبه تا پنجشنبه، ۹ تا ۱۷", "Call 02112345678"}}
}
