package bot_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestOwnerCreatesEditsAndPreviewsUnconnectedBotAcrossRestart(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	if page := b.Send("GET", "/bots/new", nil); page.Code != 200 || !strings.Contains(page.Body.String(), `name="token"`) {
		t.Fatalf("Telegram setup page: %d", page.Code)
	}
	got := b.Post("/bots/new", url.Values{"name": {"ایده <script>من</script>"}})
	if got.Code != 303 || got.Header().Get("Location") != "/bots/1/studio" {
		t.Fatalf("create: %d %s", got.Code, got.Header().Get("Location"))
	}
	loaded := b.LoadDraft(t, 1)
	if loaded.Get("draft_revision") != "1" {
		t.Fatal("creation did not initialize a saved revisioned Draft")
	}
	// The initialized snapshot must already be usable in the shared runtime.
	initial := b.Post("/bots/1/preview", url.Values{})
	if initial.Code != 303 || b.Send("GET", initial.Header().Get("Location"), nil).Code != 200 {
		t.Fatal("initial Draft cannot be previewed")
	}
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.InquiryDraft(), "1")); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")); err == nil {
		t.Fatal("rejected Draft change was accepted")
	}
	preview := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if err := a.DB.Close(); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("restart contacted Telegram")
		w.WriteHeader(500)
	}))
	defer fake.Close()
	restarted, err := fixture.NewWithTelegram(t.Context(), a.Config, telegram.NewClient(fake.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	if loaded := b.LoadDraft(t, 1); loaded.Get("draft_revision") != "2" {
		t.Fatal("restart lost the Draft revision")
	}
	for i, values := range []url.Values{{"choice": {"inquiry"}}, {"answer": {"مینا"}}, {"answer": {"۰۹۱۲۳۴۵۶۷۸۹"}}, {"choice": {"skip"}}, {"choice": {"submit"}}} {
		values.Set("revision", strconv.Itoa(i+1))
		if got := b.Post(preview+"/choose", values); got.Code != 303 {
			t.Fatalf("Preview step %d: %d", i, got.Code)
		}
	}
	if page := b.Send("GET", preview, nil); page.Code != 200 || !strings.Contains(page.Body.String(), "درخواست شما دریافت شد") {
		t.Fatal("Preview failed to confirm the isolated request")
	}
	if inbox := b.Send("GET", "/bots/1/submissions", nil); inbox.Code != 200 || strings.Contains(inbox.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created a real Submission")
	}
	for _, path := range []string{"/dashboard", "/bots", "/bots/1"} {
		page := b.Send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "هنوز متصل نشده") || !strings.Contains(page.Body.String(), "&lt;script&gt;من&lt;/script&gt;") {
			t.Fatalf("Unconnected state or escaped name missing: %s", path)
		}
	}
}

func TestUnconnectedLifecycleGuardsAndDeletionNeverContactTelegram(t *testing.T) {
	_, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات تازه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatalf("offline publication: %d", got.Code)
	}
	for _, action := range []string{"replace-token", "reconnect", "disconnect", "activate", "pause", "resume"} {
		got := b.Post("/bots/1/"+action, url.Values{"token": {fixture.TestBotToken}, "operate": {"yes"}})
		if got.Code != 409 {
			t.Fatalf("unsupported %s: %d", action, got.Code)
		}
	}
	if got := b.Send("GET", "/bots/1/activate", nil); got.Code != 409 || !strings.Contains(got.Body.String(), "هنوز به تلگرام متصل نشده") {
		t.Fatal("activation inspection lacks honest missing-connection feedback")
	}
	page := b.Send("GET", "/bots/1", nil).Body.String()
	for _, action := range []string{"replace-token", "reconnect", "disconnect", "pause", "resume"} {
		if strings.Contains(page, `action="/bots/1/`+action+`"`) {
			t.Fatalf("Unconnected Bot offers %s", action)
		}
	}
	if strings.Contains(page, "@</bdi>") || strings.Contains(page, `<bdi dir="ltr">0</bdi>`) || strings.Contains(page, "data-persian-date") {
		t.Fatal("Unconnected Bot renders fabricated Telegram identity or observation")
	}
	preview := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.Post("/bots/1/delete", url.Values{}); got.Code != 422 {
		t.Fatal("deletion did not require confirmation")
	}
	if got := b.Post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 || got.Header().Get("Location") != "/bots" {
		t.Fatalf("local deletion: %d %s", got.Code, got.Header().Get("Location"))
	}
	for _, path := range []string{"/bots/1", preview, "/bots/1/submissions"} {
		if got := b.Send("GET", path, nil); got.Code != 404 {
			t.Fatalf("retained deleted Bot data at %s: %d", path, got.Code)
		}
	}
}

func TestUnconnectedCreationValidatesAndSavesBotAndDraftAtomically(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	for _, names := range [][]string{nil, {" "}, {strings.Repeat("س", 81)}, {"line\nbreak"}, {"first", "second"}} {
		if got := b.Post("/bots/new", url.Values{"name": names}); got.Code != 422 {
			t.Fatalf("invalid name %q: %d", names, got.Code)
		}
	}
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_initial_draft BEFORE INSERT ON bot_drafts BEGIN SELECT RAISE(ABORT,'private storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/new", url.Values{"name": {"ساخت ناموفق"}}); got.Code != 500 || strings.Contains(got.Body.String(), "private storage failure") {
		t.Fatal("initial Draft failure was not handled")
	}
	if page := b.Send("GET", "/bots", nil); strings.Contains(page.Body.String(), "ساخت ناموفق") || !strings.Contains(page.Body.String(), "هنوز رباتی نساخته") {
		t.Fatal("failed Draft initialization left a partial Bot")
	}
	if _, err := a.DB.Exec("DROP TRIGGER fail_initial_draft"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ربات نخست", "ربات دوم"} {
		if got := b.Post("/bots/new", url.Values{"name": {name}}); got.Code != 303 {
			t.Fatal("multiple missing identities must be permitted")
		}
	}
	var missing bool
	if err := a.DB.QueryRow("SELECT telegram_id IS NULL FROM bots WHERE id = 1").Scan(&missing); err != nil || !missing {
		t.Fatal("Unconnected Bot has a synthetic Telegram identity")
	}
}

func TestUnconnectedWorkspaceRequiresOwnerPOSTAndCSRF(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	guest := fixture.NewAccountBrowser(t, a.Handler)
	guest.Send("GET", "/login", nil)
	for _, method := range []string{"GET", "POST"} {
		if got := guest.Send(method, "/bots/new", url.Values{"name": {"anonymous"}, "csrf_token": {guest.Cookie("piko_csrf")}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Fatalf("anonymous creation %s: %d", method, got.Code)
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		if got := b.Send("POST", "/bots/new", url.Values{"name": {"rejected"}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal("creation accepted invalid CSRF")
		}
	}
	if got := b.Send("PUT", "/bots/new", url.Values{"csrf_token": {b.Cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatal("creation accepted another method")
	}
	if got := b.Send("GET", "/bots/new?name=not-saved", nil); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", "/bots", nil); !strings.Contains(page.Body.String(), "هنوز رباتی نساخته") {
		t.Fatal("GET or rejected mutation saved a Bot")
	}
	if got := b.Post("/bots/new", url.Values{"name": {"خصوصی"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	preview := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("unconnected-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1", preview, "/bots/1/submissions", "/bots/1/activate"} {
		if got := other.Send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "خصوصی") {
			t.Fatalf("cross-owner read: %s %d", path, got.Code)
		}
	}
	posts := []string{"/bots/1/preview", preview + "/choose", preview + "/restart", "/bots/1/publish", "/bots/1/delete", "/bots/1/replace-token", "/bots/1/reconnect", "/bots/1/disconnect", "/bots/1/activate", "/bots/1/pause", "/bots/1/resume"}
	for _, path := range posts {
		v := url.Values{"token": {fixture.TestBotToken}, "confirm_delete": {"yes"}, "operate": {"yes"}}
		if got := other.Post(path, v); got.Code != 404 {
			t.Fatalf("cross-owner mutation: %s %d", path, got.Code)
		}
		if got := b.Send("POST", path, v); got.Code != 403 {
			t.Fatalf("CSRF mutation: %s %d", path, got.Code)
		}
		v.Set("csrf_token", b.Cookie("piko_csrf"))
		if got := b.Send("PUT", path, v); got.Code != 405 {
			t.Fatalf("non-POST mutation: %s %d", path, got.Code)
		}
	}
	if page := other.Send("GET", "/bots", nil); strings.Contains(page.Body.String(), "خصوصی") {
		t.Fatal("list exposed another owner's Bot")
	}
	if got := other.Post("/bots/new", url.Values{"name": {"ربات دیگری"}}); got.Code != 303 || got.Header().Get("Location") != "/bots/2/studio" {
		t.Fatal("creation did not retain the signed-in owner")
	}
	if page := b.Send("GET", "/bots/2", nil); page.Code != 404 {
		t.Fatal("first owner can access the second owner's Bot")
	}
}

func TestUnconnectedRollbackRefusesToDiscardWorkAndRestoresForeignKeys(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"کار محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	preview := fixture.StartLegacyPreview(t, a, b)
	// Arrange retained work before removing columns used by today's server.
	// Exercise the Bot-table rebuild, independently of later migrations.
	fixture.RollbackToMigration(t, a.DB, "000011_unconnected_bots")
	before := b.LoadDraft(t, 1)
	if err := database.Migrate(t.Context(), a.DB, true); err == nil {
		t.Fatal("rollback discarded an Unconnected Bot")
	}
	if current := b.LoadDraft(t, 1); !reflect.DeepEqual(current, before) {
		t.Fatal("failed rollback changed saved work")
	}
	var foreignKeys int
	if err := a.DB.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("failed migration left foreign keys disabled")
	}
	if got := b.Post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Send("GET", preview, nil); got.Code != 404 {
		t.Fatal("foreign-key cascades were not restored")
	}
	if err := database.Migrate(t.Context(), a.DB, true); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/new", url.Values{"name": {"دوباره"}}); got.Code != 303 || got.Header().Get("Location") != "/bots/2/studio" {
		t.Fatal("rebuild lost the Bot ID high-water mark")
	}
}
