package app

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

// Migration fixtures model historical snapshots whose source was never tracked.
func startLegacyPreview(t *testing.T, a *App, b *fixture.Browser) string {
	t.Helper()
	got := b.Post("/bots/1/preview", url.Values{})
	if got.Code != 303 {
		t.Fatalf("legacy Preview setup: %d", got.Code)
	}
	if _, err := a.db.Exec("UPDATE bot_previews SET source_revision = 0"); err != nil {
		t.Fatal(err)
	}
	return got.Header().Get("Location")
}

func TestStudioPreviewRemainsIndependentForUnconnectedAndPausedBots(t *testing.T) {
	for _, state := range []string{"unconnected", "paused"} {
		t.Run(state, func(t *testing.T) {
			var b *fixture.Browser
			if state == "unconnected" {
				_, b = unconnectedFixture(t)
				b.Post("/bots/new", url.Values{"name": {"ربات آزمایش"}})
				b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft())
			} else {
				d := newInquiryDriver(t)
				b = d.b
				if got := b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			}
			b.Post("/bots/1/chats", url.Values{"title": {"گفتگوی آزمایش"}})
			path := fixture.PreviewRequest(b, "POST", "/bots/1/preview", nil).Header().Get("Location")
			for i, v := range []url.Values{{"choice": {"inquiry"}}, {"answer": {"مینا"}}, {"answer": {"09123456789"}}, {"choice": {"skip"}}, {"choice": {"cancel"}}} {
				v.Set("revision", strconv.Itoa(i+1))
				if got := fixture.PreviewRequest(b, "POST", path+"/choose", v); got.Code != 303 {
					t.Fatalf("%s Preview step %d: %d", state, i, got.Code)
				}
			}
			if got := fixture.PreviewRequest(b, "GET", path, nil); !strings.Contains(got.Body.String(), "لغو") {
				t.Fatal("Preview did not cancel the isolated interaction")
			}
			if page := fixture.StudioRequest(b, "GET", "/bots/1/chats/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "data-studio-preview") {
				t.Fatal("studio Preview unavailable")
			}
			if inbox := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(inbox.Body.String(), "data-submission-id=") {
				t.Fatal("isolated cancellation created a Submission")
			}
		})
	}
}
