package app

import (
	"database/sql"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
)

func TestManualDraftEditorsRejectStaleSavesIncludingFirstSave(t *testing.T) {
	_, b := draftFixture(t)
	path := "/bots/1/draft"
	first := renderedDraft(t, b.send("GET", path, nil).Body.String())
	second := renderedDraft(t, b.send("GET", path, nil).Body.String())
	if first.Get("draft_revision") != "0" || second.Get("draft_revision") != "0" {
		t.Fatal("unsaved editors must load revision zero")
	}
	v := welcomeDraft()
	v.Set("draft_revision", first.Get("draft_revision"))
	if got := b.post(path, v); got.Code != 303 {
		t.Fatalf("first save: %d", got.Code)
	}
	for _, revision := range []string{"0", "1"} {
		if revision == "1" {
			first = renderedDraft(t, b.send("GET", path, nil).Body.String())
			second = renderedDraft(t, b.send("GET", path, nil).Body.String())
			first.Set("welcome", "Newer saved welcome")
			if got := b.post(path, first); got.Code != 303 {
				t.Fatalf("newer save: %d", got.Code)
			}
		}
		stale := welcomeDraft()
		stale.Set("welcome", "Stale unsaved welcome")
		stale.Set("draft_revision", second.Get("draft_revision"))
		got := b.post(path, stale)
		if got.Code != 409 || !strings.Contains(got.Body.String(), `role="alert"`) || !strings.Contains(got.Body.String(), "Stale unsaved welcome") {
			t.Fatalf("stale revision %s: %d", revision, got.Code)
		}
		if renderedDraft(t, got.Body.String()).Get("draft_revision") != revision {
			t.Fatal("conflict silently rebased the editor")
		}
		if retry := b.post(path, renderedDraft(t, got.Body.String())); retry.Code != 409 {
			t.Fatalf("stale retry: %d", retry.Code)
		}
	}
	loaded := renderedDraft(t, b.send("GET", path, nil).Body.String())
	if loaded.Get("draft_revision") != "2" || loaded.Get("welcome") != "Newer saved welcome" {
		t.Fatal("stale save changed the current Draft or revision")
	}
	loaded.Set("welcome", "Owner reloaded and saved")
	if got := b.post(path, loaded); got.Code != 303 {
		t.Fatalf("reloaded save: %d", got.Code)
	}
	loaded = renderedDraft(t, b.send("GET", path, nil).Body.String())
	if loaded.Get("draft_revision") != "3" || loaded.Get("welcome") != "Owner reloaded and saved" {
		t.Fatal("successful save did not advance the revision")
	}
}

// Supply an explicitly loaded revision when sending a candidate definition.
func draftAtRevision(v url.Values, revision string) url.Values {
	v.Set("draft_revision", revision)
	return v
}

func TestDraftValidationAndEditorActionsKeepLoadedRevision(t *testing.T) {
	_, b := draftFixture(t)
	path := "/bots/1/draft"
	if got := b.post(path, draftAtRevision(welcomeDraft(), "0")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	loaded := renderedDraft(t, b.send("GET", path, nil).Body.String())
	invalid := draftAtRevision(welcomeDraft(), "1")
	invalid.Set("welcome", "")
	got := b.post(path, invalid)
	if got.Code != 422 || renderedDraft(t, got.Body.String()).Get("draft_revision") != "1" {
		t.Fatalf("invalid editor lost its revision: %d", got.Code)
	}
	if current := renderedDraft(t, b.send("GET", path, nil).Body.String()); !reflect.DeepEqual(current, loaded) {
		t.Fatal("validation changed the saved Draft or revision")
	}
	loaded.Set("edit", "add:inquiry")
	got = b.post(path, loaded)
	if got.Code != 200 {
		t.Fatal(got.Code)
	}
	edited := renderedDraft(t, got.Body.String())
	if edited.Get("draft_revision") != "1" || len(edited["form_id"]) != 1 {
		t.Fatal("unsaved catalog action lost its base revision")
	}
	if got := b.post(path, draftAtRevision(welcomeDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	// The same content saved again still advances the revision, avoiding ABA edits.
	if got := b.post(path, edited); got.Code != 409 {
		t.Fatalf("unsaved catalog action overwrote newer Draft: %d", got.Code)
	}
}

func TestDraftRevisionRequiresOwnerPOSTAndCSRF(t *testing.T) {
	_, b := draftFixture(t)
	path := "/bots/1/draft"
	if got := b.post(path, draftAtRevision(welcomeDraft(), "0")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := renderedDraft(t, b.send("GET", path, nil).Body.String())
	for _, revision := range [][]string{nil, {"-1"}, {"bad"}, {"9223372036854775808"}, {"1", "1"}} {
		v := welcomeDraft()
		v["draft_revision"] = revision
		// Use the browser's real CSRF cookie without automatic revision loading.
		v.Set("csrf_token", before.Get("csrf_token"))
		if got := b.send("POST", path, v); got.Code != 422 {
			t.Fatalf("malformed revision %v: %d", revision, got.Code)
		}
	}
	for _, revision := range []string{"0", "2", "9223372036854775807"} {
		if got := b.post(path, draftAtRevision(welcomeDraft(), revision)); got.Code != 409 {
			t.Fatalf("non-current revision %s: %d", revision, got.Code)
		}
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE"} {
		if got := b.send(method, path, before); got.Code != 405 && got.Code != 403 {
			t.Fatalf("%s accepted mutation: %d", method, got.Code)
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		v := draftAtRevision(welcomeDraft(), "1")
		v.Set("csrf_token", csrf)
		if got := b.send("POST", path, v); got.Code != 403 {
			t.Fatalf("invalid CSRF: %d", got.Code)
		}
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("revision-other@example.test", "دیگری", "Original123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, method := range []string{"GET", "POST"} {
		v := draftAtRevision(welcomeDraft(), "1")
		if method == "POST" {
			if got := other.post(path, v); got.Code != 404 {
				t.Fatal(got.Code)
			}
		} else if got := other.send(method, path, nil); got.Code != 404 {
			t.Fatal(got.Code)
		}
	}
	guest := newAccountBrowser(t, b.router)
	guest.send("GET", "/login", nil)
	if got := guest.post(path, draftAtRevision(welcomeDraft(), "1")); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous save: %d", got.Code)
	}
	if current := renderedDraft(t, b.send("GET", path, nil).Body.String()); !reflect.DeepEqual(current, before) {
		t.Fatal("rejected action changed saved Draft or revision")
	}
}

func TestConcurrentDraftSavesAcrossAppsCommitOneRevision(t *testing.T) {
	a, b := draftFixture(t)
	second, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.db.Close() })
	other := newAccountBrowser(t, second.server.Handler)
	other.jar.SetCookies(other.base, b.jar.Cookies(b.base))
	path := "/bots/1/draft"
	for _, revision := range []string{"0", "1"} {
		start := make(chan struct{})
		type outcome struct {
			status  int
			welcome string
		}
		results := make(chan outcome, 2)
		for i, browser := range []*accountBrowser{b, other} {
			v := draftAtRevision(welcomeDraft(), revision)
			v.Set("welcome", []string{"First editor", "Second editor"}[i])
			go func() {
				<-start
				results <- outcome{browser.post(path, v).Code, v.Get("welcome")}
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
		loaded := renderedDraft(t, b.send("GET", path, nil).Body.String())
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
	a, b := draftFixture(t)
	path := "/bots/1/draft"
	if got := b.post(path, draftAtRevision(welcomeDraft(), "0")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := renderedDraft(t, b.send("GET", path, nil).Body.String())
	if _, err := a.db.Exec("CREATE TRIGGER fail_draft BEFORE UPDATE ON bot_drafts BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
		t.Fatal(err)
	}
	v := draftAtRevision(welcomeDraft(), "1")
	v.Set("welcome", "Failed candidate")
	if got := b.post(path, v); got.Code != 500 {
		t.Fatal(got.Code)
	}
	if current := renderedDraft(t, b.send("GET", path, nil).Body.String()); !reflect.DeepEqual(current, before) {
		t.Fatal("failed storage changed the Draft or revision")
	}
	if _, err := a.db.Exec("DROP TRIGGER fail_draft"); err != nil {
		t.Fatal(err)
	}
	if got := b.post(path, v); got.Code != 303 {
		t.Fatal(got.Code)
	}
}

func TestDraftRevisionUpgradeRetainsOwnerAndBotData(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست پیشین", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پیشرفت پیشین", 1)
	preview := startLegacyPreview(t, d.a, d.b)
	before := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String())
	priorRevision := before.Get("draft_revision")
	before.Set("draft_revision", "1") // Existing pre-revision Drafts begin at one.
	submission := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String()
	previewBody := d.b.send("GET", preview, nil).Body.String()
	// The pre-revision upgrade also changes the current Draft metadata shown
	// alongside the retained legacy Preview, without changing its snapshot.
	previewBody = strings.Replace(previewBody, `data-preview-draft-revision="`+priorRevision+`"`, `data-preview-draft-revision="1"`, 1)
	stop()
	legacy, err := database.Open(t.Context(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	// Drop only the new column in this disposable file to represent version 9.
	rollbackToMigration(t, legacy, "000009_bot_pause")
	for range 2 {
		if err := database.Migrate(t.Context(), legacy, false); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	if got := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(got, before) || got.Get("draft_revision") != "1" {
		t.Fatal("upgrade changed the saved Draft, owner session or initial revision")
	}
	if got := d.b.send("GET", "/bots/1/submissions/1", nil); got.Code != 200 || got.Body.String() != submission {
		t.Fatal("upgrade changed collected Submission")
	}
	if got := d.b.send("GET", preview, nil); got.Code != 200 || got.Body.String() != previewBody {
		t.Fatal("upgrade changed Preview snapshot")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("upgrade lost published version, credentials or unfinished answers")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if page := d.b.send("GET", "/bots/1/submissions/2", nil); !strings.Contains(page.Body.String(), "پیشرفت پیشین") {
		t.Fatal("upgrade lost unfinished answers")
	}
	if got := d.b.post("/bots/1/draft", draftAtRevision(welcomeDraft(), "0")); got.Code != 409 {
		t.Fatal("pre-upgrade unsaved editor replaced migrated Draft")
	}
	if got := d.b.post("/bots/1/draft", before); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String()); got.Get("draft_revision") != "2" {
		t.Fatal("migrated Draft did not advance")
	}
}

// Migration setup stops at an explicit schema version as new migrations are added.
func rollbackToMigration(t *testing.T, db *sql.DB, target string) {
	t.Helper()
	for {
		var latest string
		if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&latest); err != nil {
			t.Fatal(err)
		}
		if latest == target {
			return
		}
		if latest < target {
			t.Fatalf("migration %s is older than target %s", latest, target)
		}
		if err := database.Migrate(t.Context(), db, true); err != nil {
			t.Fatal(err)
		}
	}
}
