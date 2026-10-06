package bot_test

import (
	"reflect"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestManualDraftEditorsRejectStaleSavesIncludingFirstSave(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	path := "/bots/1/draft"
	first := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	second := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	if first.Get("draft_revision") != "0" || second.Get("draft_revision") != "0" {
		t.Fatal("unsaved editors must load revision zero")
	}
	v := fixture.WelcomeDraft()
	v.Set("draft_revision", first.Get("draft_revision"))
	if got := b.Post(path, v); got.Code != 303 {
		t.Fatalf("first save: %d", got.Code)
	}
	for _, revision := range []string{"0", "1"} {
		if revision == "1" {
			first = fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
			second = fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
			first.Set("welcome", "Newer saved welcome")
			if got := b.Post(path, first); got.Code != 303 {
				t.Fatalf("newer save: %d", got.Code)
			}
		}
		stale := fixture.WelcomeDraft()
		stale.Set("welcome", "Stale unsaved welcome")
		stale.Set("draft_revision", second.Get("draft_revision"))
		got := b.Post(path, stale)
		if got.Code != 409 || !strings.Contains(got.Body.String(), `role="alert"`) || !strings.Contains(got.Body.String(), "Stale unsaved welcome") {
			t.Fatalf("stale revision %s: %d", revision, got.Code)
		}
		if fixture.RenderedDraft(t, got.Body.String()).Get("draft_revision") != revision {
			t.Fatal("conflict silently rebased the editor")
		}
		if retry := b.Post(path, fixture.RenderedDraft(t, got.Body.String())); retry.Code != 409 {
			t.Fatalf("stale retry: %d", retry.Code)
		}
	}
	loaded := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	if loaded.Get("draft_revision") != "2" || loaded.Get("welcome") != "Newer saved welcome" {
		t.Fatal("stale save changed the current Draft or revision")
	}
	loaded.Set("welcome", "Owner reloaded and saved")
	if got := b.Post(path, loaded); got.Code != 303 {
		t.Fatalf("reloaded save: %d", got.Code)
	}
	loaded = fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	if loaded.Get("draft_revision") != "3" || loaded.Get("welcome") != "Owner reloaded and saved" {
		t.Fatal("successful save did not advance the revision")
	}
}

func TestDraftValidationAndEditorActionsKeepLoadedRevision(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	path := "/bots/1/draft"
	if got := b.Post(path, fixture.DraftAtRevision(fixture.WelcomeDraft(), "0")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	loaded := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	invalid := fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")
	invalid.Set("welcome", "")
	got := b.Post(path, invalid)
	if got.Code != 422 || fixture.RenderedDraft(t, got.Body.String()).Get("draft_revision") != "1" {
		t.Fatalf("invalid editor lost its revision: %d", got.Code)
	}
	if current := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String()); !reflect.DeepEqual(current, loaded) {
		t.Fatal("validation changed the saved Draft or revision")
	}
	loaded.Set("edit", "add:inquiry")
	got = b.Post(path, loaded)
	if got.Code != 200 {
		t.Fatal(got.Code)
	}
	edited := fixture.RenderedDraft(t, got.Body.String())
	if edited.Get("draft_revision") != "1" || len(edited["form_id"]) != 1 {
		t.Fatal("unsaved catalog action lost its base revision")
	}
	if got := b.Post(path, fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	// The same content saved again still advances the revision, avoiding ABA edits.
	if got := b.Post(path, edited); got.Code != 409 {
		t.Fatalf("unsaved catalog action overwrote newer Draft: %d", got.Code)
	}
}

func TestDraftRevisionRequiresOwnerPOSTAndCSRF(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	path := "/bots/1/draft"
	if got := b.Post(path, fixture.DraftAtRevision(fixture.WelcomeDraft(), "0")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	for _, revision := range [][]string{nil, {"-1"}, {"bad"}, {"9223372036854775808"}, {"1", "1"}} {
		v := fixture.WelcomeDraft()
		v["draft_revision"] = revision
		// Use the browser's real CSRF cookie without automatic revision loading.
		v.Set("csrf_token", before.Get("csrf_token"))
		if got := b.Send("POST", path, v); got.Code != 422 {
			t.Fatalf("malformed revision %v: %d", revision, got.Code)
		}
	}
	for _, revision := range []string{"0", "2", "9223372036854775807"} {
		if got := b.Post(path, fixture.DraftAtRevision(fixture.WelcomeDraft(), revision)); got.Code != 409 {
			t.Fatalf("non-current revision %s: %d", revision, got.Code)
		}
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		if got := b.Send(method, path, before); got.Code != 405 && got.Code != 403 {
			t.Fatalf("%s accepted mutation: %d", method, got.Code)
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		v := fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")
		v.Set("csrf_token", csrf)
		if got := b.Send("POST", path, v); got.Code != 403 {
			t.Fatalf("invalid CSRF: %d", got.Code)
		}
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("revision-other@example.test", "دیگری", "Original123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		v := fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")
		if method == "POST" {
			if got := other.Post(path, v); got.Code != 404 {
				t.Fatal(got.Code)
			}
		} else if got := other.Send(method, path, nil); got.Code != 404 {
			t.Fatal(got.Code)
		}
	}
	guest := fixture.NewAccountBrowser(t, b.Router)
	guest.Send("GET", "/login", nil)
	if got := guest.Post(path, fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous save: %d", got.Code)
	}
	if current := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String()); !reflect.DeepEqual(current, before) {
		t.Fatal("rejected action changed saved Draft or revision")
	}
}

func TestConcurrentDraftSavesAcrossAppsCommitOneRevision(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	second, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.DB.Close() })
	other := fixture.NewAccountBrowser(t, second.Handler)
	other.Jar.SetCookies(other.Base, b.Jar.Cookies(b.Base))
	path := "/bots/1/draft"
	for _, revision := range []string{"0", "1"} {
		start := make(chan struct{})
		type outcome struct {
			status  int
			welcome string
		}
		results := make(chan outcome, 2)
		for i, browser := range []*fixture.Browser{b, other} {
			v := fixture.DraftAtRevision(fixture.WelcomeDraft(), revision)
			v.Set("welcome", []string{"First editor", "Second editor"}[i])
			go func() {
				<-start
				results <- outcome{browser.Post(path, v).Code, v.Get("welcome")}
			}()
		}
		close(start)
		winner, loser := <-results, <-results
		if winner.status == 409 {
			winner, loser = loser, winner
		}
		if winner.status != 303 || loser.status != 409 {
			t.Fatalf("concurrent revision %s: %d/%d", revision, winner.status, loser.status)
		}
		loaded := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
		wantRevision := "1"
		if revision == "1" {
			wantRevision = "2"
		}
		if loaded.Get("draft_revision") != wantRevision || loaded.Get("welcome") != winner.welcome {
			t.Fatal("concurrent save changed the winning Draft")
		}
	}
}

func TestDraftRevisionStorageFailurePreservesSavedSnapshot(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	path := "/bots/1/draft"
	if got := b.Post(path, fixture.DraftAtRevision(fixture.WelcomeDraft(), "0")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String())
	if _, err := a.DB.Exec("CREATE TRIGGER fail_draft BEFORE UPDATE ON bot_drafts BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
		t.Fatal(err)
	}
	v := fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")
	v.Set("welcome", "Failed candidate")
	if got := b.Post(path, v); got.Code != 500 {
		t.Fatal(got.Code)
	}
	if current := fixture.RenderedDraft(t, b.Send("GET", path, nil).Body.String()); !reflect.DeepEqual(current, before) {
		t.Fatal("failed storage changed the Draft or revision")
	}
	if _, err := a.DB.Exec("DROP TRIGGER fail_draft"); err != nil {
		t.Fatal(err)
	}
	if got := b.Post(path, v); got.Code != 303 {
		t.Fatal(got.Code)
	}
}
