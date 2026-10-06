package bot_test

import (
	"net/url"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestFlowTabInspectsDraftOutsideStudio(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	page := b.Send("GET", "/bots/1/flow", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-flow-view="draft"`) || !strings.Contains(page.Body.String(), `data-flow-revision="0"`) {
		t.Fatalf("empty Flow tab: status %d", page.Code)
	}
	if err := b.SaveDraft(t, 1, fixture.InquiryDraft()); err != nil {
		t.Fatal(err)
	}
	page = b.Send("GET", "/bots/1/flow", nil)
	for _, want := range []string{`data-flow-revision="1"`, `data-flow-key="question:aW5xdWlyeQ:bmFtZQ"`, "نام شما چیست؟", `href="/bots/1/flow?view=published"`, `id="flow-open-piko"`, `href="/bots/1/studio"`} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Flow missing %q: status %d", want, page.Code)
		}
	}
	studio := b.Send("GET", "/bots/1/studio", nil).Body.String()
	if strings.Contains(studio, `data-studio-flow`) || strings.Contains(studio, `id="studio-tab-flow"`) {
		t.Fatal("Flow remains embedded in Studio")
	}
}

func TestFlowViewsKeepDraftSeparateFromPublishedAndOwnerScoped(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	if page := b.Send("GET", "/bots/1/flow?view=published", nil); page.Code != 200 || !strings.Contains(page.Body.String(), `data-flow-revision="0"`) {
		t.Fatal("unpublished Bot should have an empty Published view")
	}
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if err := b.SaveDraft(t, 1, fixture.InquiryDraft()); err != nil {
		t.Fatal(err)
	}
	draft := b.Send("GET", "/bots/1/flow", nil).Body.String()
	published := b.Send("GET", "/bots/1/flow?view=published", nil).Body.String()
	if !strings.Contains(draft, "نام شما چیست؟") || !strings.Contains(draft, `data-flow-revision="2"`) {
		t.Fatal("Draft view did not show the saved edit")
	}
	if !strings.Contains(published, "Call 02112345678") || strings.Contains(published, "نام شما چیست؟") || !strings.Contains(published, `data-flow-view="published"`) || !strings.Contains(published, `data-flow-revision="1"`) {
		t.Fatal("Published view followed Draft changes")
	}
	for _, tc := range []struct{ path, revision string }{{"/bots/1/flow/status", `"revision":2`}, {"/bots/1/flow/status?view=published", `"revision":1`}} {
		got := b.Send("GET", tc.path, nil)
		if got.Code != 200 || !strings.Contains(got.Body.String(), tc.revision) || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("Flow status %s: %d %s", tc.path, got.Code, got.Body.String())
		}
	}
	for _, path := range []string{"/bots/1/flow?view=unknown", "/bots/0/flow"} {
		if got := b.Send("GET", path, nil); got.Code != 404 {
			t.Fatal(got.Code)
		}
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("flow-reader@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/flow", "/bots/1/flow?view=published", "/bots/1/flow/status", "/bots/1/flow/status?view=published"} {
		got := other.Send("GET", path, nil)
		if got.Code != 404 || strings.Contains(got.Body.String(), "Call 02112345678") || strings.Contains(got.Body.String(), "نام شما چیست؟") {
			t.Fatalf("Flow owner boundary: %s %d", path, got.Code)
		}
	}
	if got := b.Post("/bots/1/flow", url.Values{}); got.Code != 405 {
		t.Fatalf("Flow inspection must not accept edits: %d", got.Code)
	}
}

func TestFlowPikoButtonOpensLatestBotChatWithoutSelection(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	fixture.SeedBotChat(t, a.DB, 1, "گفتگوی اول")
	fixture.SeedBotChat(t, a.DB, 1, "گفتگوی دوم")
	page := b.Send("GET", "/bots/1/flow", nil).Body.String()
	if !strings.Contains(page, `id="flow-open-piko"`) || !strings.Contains(page, `href="/bots/1/chats/2"`) || strings.Contains(page, "selected_block") || strings.Contains(page, "data-flow-change") {
		t.Fatal("Piko should open the latest Bot chat without a targeted request")
	}
}
