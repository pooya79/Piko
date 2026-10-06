package builder_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestStudioPreviewUsesInteractiveFragmentsWithValidationAndIsolation(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft())
	fixture.SeedBotChat(t, a.DB, 1, "آزمایش گفتگو")
	studio := fixture.StudioRequest(b, "GET", "/bots/1/chats/1", nil)
	if studio.Code != 200 || !strings.Contains(studio.Body.String(), `data-studio-preview`) || !strings.Contains(studio.Body.String(), `action="/bots/1/preview"`) {
		t.Fatal("studio has no embedded Preview launch")
	}
	path := fixture.PreviewRequest(b, "POST", "/bots/1/preview", nil).Header().Get("Location")
	got := fixture.PreviewRequest(b, "GET", path, nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `data-preview-url="`+path+`"`) || strings.Contains(got.Body.String(), "<html") || strings.Contains(got.Body.String(), "<script") {
		t.Fatal("Preview did not return a pane fragment")
	}
	got = fixture.PreviewRequest(b, "POST", path+"/choose", url.Values{"revision": {"1"}, "choice": {"inquiry"}})
	if got.Code != 303 {
		t.Fatal(got.Code)
	}
	got = fixture.PreviewRequest(b, "GET", path, nil)
	if !strings.Contains(got.Body.String(), "نام شما چیست؟") || !strings.Contains(got.Body.String(), `name="answer"`) {
		t.Fatal("interactive question missing from pane")
	}
	for i, values := range []url.Values{{"answer": {"مینا"}}, {"choice": {"back"}}, {"answer": {"سارا"}}, {"answer": {"۰۹۱۲۳۴۵۶۷۸۹"}}, {"choice": {"skip"}}, {"choice": {"edit"}}, {"choice": {"edit:name"}}, {"answer": {"نام نهایی"}}, {"choice": {"submit"}}} {
		values.Set("revision", strconv.Itoa(i+2))
		if got := fixture.PreviewRequest(b, "POST", path+"/choose", values); got.Code != 303 {
			t.Fatalf("pane interaction %d: %d", i, got.Code)
		}
	}
	if got := fixture.PreviewRequest(b, "GET", path, nil); !strings.Contains(got.Body.String(), "درخواست شما دریافت شد") {
		t.Fatal("pane confirmation missing")
	}
	if got := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(got.Body.String(), "data-submission-id=") {
		t.Fatal("pane created a real Submission")
	}
	got = fixture.PreviewRequest(b, "POST", path+"/choose", url.Values{"revision": {"1"}, "choice": {"cancel"}})
	if got.Code != 409 || !strings.Contains(got.Body.String(), `role="alert"`) || strings.Contains(got.Body.String(), "<html") {
		t.Fatal("conflict did not return honest pane feedback")
	}
	if got := fixture.PreviewRequest(b, "POST", path+"/restart", url.Values{"revision": {"2"}, "csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("pane bypassed CSRF")
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("other-preview@example.test", "Other", "OwnerPassword123"))
	if got := fixture.PreviewRequest(other, "GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "نام شما چیست؟") {
		t.Fatal("pane leaked another owner's Preview")
	}
}

func TestPreviewSourceRevisionMigrationPreservesLegacySnapshotAndProgress(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	b.PostDraft(t, "/bots/1/draft", fixture.WelcomeDraft())
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	b.Post(path+"/choose", url.Values{"choice": {"2"}, "revision": {"1"}})
	// Model a populated pre-upgrade file without inventing historical revisions.
	fixture.RollbackToMigration(t, a.DB, "000019_piko_chat_starts")
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	got := b.Send("GET", path, nil)
	for _, want := range []string{`data-preview-source-revision="0"`, `data-preview-stale="true"`, `data-preview-revision="2"`, "Call 02112345678", "مشخص نیست"} {
		if got.Code != 200 || !strings.Contains(got.Body.String(), want) {
			t.Fatalf("migration/restart lost %s", want)
		}
	}
	if got := b.Post(path+"/restart", url.Values{"revision": {"2"}}); got.Code != 303 {
		t.Fatal("legacy snapshot cannot restart")
	}
	if got := b.Send("GET", path, nil); strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("legacy restart did not clear progress")
	}
}

func TestPreviewIdentifiesSourceRevisionIndependentlyOfProgress(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	b.PostDraft(t, "/bots/1/draft", fixture.WelcomeDraft())
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	assertPreview := func(source, progress, stale string) {
		t.Helper()
		got := b.Send("GET", path, nil)
		for _, want := range []string{`data-preview-source-revision="` + source + `"`, `data-preview-revision="` + progress + `"`, `data-preview-stale="` + stale + `"`} {
			if got.Code != 200 || !strings.Contains(got.Body.String(), want) {
				t.Fatalf("Preview missing %s (status %d)", want, got.Code)
			}
		}
	}
	assertPreview("1", "1", "false")
	b.Post(path+"/choose", url.Values{"choice": {"2"}, "revision": {"1"}})
	assertPreview("1", "2", "false")
	changed := fixture.WelcomeDraft()
	changed.Set("welcome", "پیام تازه")
	b.PostDraft(t, "/bots/1/draft", changed)
	if err := a.DB.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	assertPreview("1", "2", "true")
	if got := b.Post(path+"/restart", url.Values{"revision": {"2"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	assertPreview("1", "3", "true")
	if got := b.Send("GET", path, nil); strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("restart substituted the new Draft")
	}
	path = b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	assertPreview("2", "1", "false")
	if got := b.Send("GET", path, nil); !strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("fresh test did not use the latest saved Draft")
	}
}
