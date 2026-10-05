package app

import (
	"github.com/pooya79/Piko/internal/bot/telegram"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
)

func unconnectedFixture(t *testing.T) (*App, *accountBrowser) {
	t.Helper()
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Unconnected Bot work contacted Telegram")
		w.WriteHeader(500)
	})
	return a, b
}

func TestOwnerCreatesEditsAndPreviewsUnconnectedBotAcrossRestart(t *testing.T) {
	a, b := unconnectedFixture(t)
	if page := b.send("GET", "/bots/new", nil); page.Code != 200 || !strings.Contains(page.Body.String(), `name="token"`) {
		t.Fatalf("Telegram setup page: %d", page.Code)
	}
	got := b.post("/bots/new", url.Values{"name": {"ایده <script>من</script>"}})
	if got.Code != 303 || got.Header().Get("Location") != "/bots/1/draft" {
		t.Fatalf("create: %d %s", got.Code, got.Header().Get("Location"))
	}
	loaded := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if loaded.Get("draft_revision") != "1" {
		t.Fatal("creation did not initialize a saved revisioned Draft")
	}
	// The initialized snapshot must already be usable in the shared runtime.
	initial := b.post("/bots/1/preview", url.Values{})
	if initial.Code != 303 || b.send("GET", initial.Header().Get("Location"), nil).Code != 200 {
		t.Fatal("initial Draft cannot be previewed")
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatalf("edit: %d", got.Code)
	}
	if got := b.post("/bots/1/draft", draftAtRevision(welcomeDraft(), "1")); got.Code != 409 {
		t.Fatal("Unconnected Draft allowed stale edits")
	}
	preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("restart contacted Telegram")
		w.WriteHeader(500)
	}))
	defer fake.Close()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(fake.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if loaded := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); loaded.Get("draft_revision") != "2" {
		t.Fatal("restart lost the Draft revision")
	}
	for i, values := range []url.Values{{"choice": {"inquiry"}}, {"answer": {"مینا"}}, {"answer": {"۰۹۱۲۳۴۵۶۷۸۹"}}, {"choice": {"skip"}}, {"choice": {"submit"}}} {
		values.Set("revision", strconv.Itoa(i+1))
		if got := b.post(preview+"/choose", values); got.Code != 303 {
			t.Fatalf("Preview step %d: %d", i, got.Code)
		}
	}
	if page := b.send("GET", preview, nil); page.Code != 200 || !strings.Contains(page.Body.String(), "درخواست شما دریافت شد") {
		t.Fatal("Preview failed to confirm the isolated request")
	}
	if inbox := b.send("GET", "/bots/1/submissions", nil); inbox.Code != 200 || strings.Contains(inbox.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created a real Submission")
	}
	for _, path := range []string{"/dashboard", "/bots", "/bots/1"} {
		page := b.send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "هنوز متصل نشده") || !strings.Contains(page.Body.String(), "&lt;script&gt;من&lt;/script&gt;") {
			t.Fatalf("Unconnected state or escaped name missing: %s", path)
		}
	}
}

func TestUnconnectedLifecycleGuardsAndDeletionNeverContactTelegram(t *testing.T) {
	_, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"ربات تازه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatalf("offline publication: %d", got.Code)
	}
	for _, action := range []string{"replace-token", "reconnect", "disconnect", "activate", "pause", "resume"} {
		got := b.post("/bots/1/"+action, url.Values{"token": {testBotToken}, "operate": {"yes"}})
		if got.Code != 409 {
			t.Fatalf("unsupported %s: %d", action, got.Code)
		}
	}
	if got := b.send("GET", "/bots/1/activate", nil); got.Code != 409 || !strings.Contains(got.Body.String(), "هنوز به تلگرام متصل نشده") {
		t.Fatal("activation inspection lacks honest missing-connection feedback")
	}
	page := b.send("GET", "/bots/1", nil).Body.String()
	for _, action := range []string{"replace-token", "reconnect", "disconnect", "pause", "resume"} {
		if strings.Contains(page, `action="/bots/1/`+action+`"`) {
			t.Fatalf("Unconnected Bot offers %s", action)
		}
	}
	if strings.Contains(page, "@</bdi>") || strings.Contains(page, `<bdi dir="ltr">0</bdi>`) || strings.Contains(page, "data-persian-date") {
		t.Fatal("Unconnected Bot renders fabricated Telegram identity or observation")
	}
	preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.post("/bots/1/delete", url.Values{}); got.Code != 422 {
		t.Fatal("deletion did not require confirmation")
	}
	if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 || got.Header().Get("Location") != "/bots" {
		t.Fatalf("local deletion: %d %s", got.Code, got.Header().Get("Location"))
	}
	for _, path := range []string{"/bots/1", "/bots/1/draft", preview, "/bots/1/submissions"} {
		if got := b.send("GET", path, nil); got.Code != 404 {
			t.Fatalf("retained deleted Bot data at %s: %d", path, got.Code)
		}
	}
}

func TestUnconnectedCreationValidatesAndSavesBotAndDraftAtomically(t *testing.T) {
	a, b := unconnectedFixture(t)
	for _, names := range [][]string{nil, {" "}, {strings.Repeat("س", 81)}, {"line\nbreak"}, {"first", "second"}} {
		if got := b.post("/bots/new", url.Values{"name": names}); got.Code != 422 {
			t.Fatalf("invalid name %q: %d", names, got.Code)
		}
	}
	if _, err := a.db.Exec(`CREATE TRIGGER fail_initial_draft BEFORE INSERT ON bot_drafts BEGIN SELECT RAISE(ABORT,'private storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/new", url.Values{"name": {"ساخت ناموفق"}}); got.Code != 500 || strings.Contains(got.Body.String(), "private storage failure") {
		t.Fatal("initial Draft failure was not handled")
	}
	if page := b.send("GET", "/bots", nil); strings.Contains(page.Body.String(), "ساخت ناموفق") || !strings.Contains(page.Body.String(), "هنوز رباتی نساخته") {
		t.Fatal("failed Draft initialization left a partial Bot")
	}
	if _, err := a.db.Exec("DROP TRIGGER fail_initial_draft"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ربات نخست", "ربات دوم"} {
		if got := b.post("/bots/new", url.Values{"name": {name}}); got.Code != 303 {
			t.Fatal("multiple missing identities must be permitted")
		}
	}
	var missing bool
	if err := a.db.QueryRow("SELECT telegram_id IS NULL FROM bots WHERE id = 1").Scan(&missing); err != nil || !missing {
		t.Fatal("Unconnected Bot has a synthetic Telegram identity")
	}
}

func TestUnconnectedWorkspaceRequiresOwnerPOSTAndCSRF(t *testing.T) {
	a, b := unconnectedFixture(t)
	guest := newAccountBrowser(t, a.server.Handler)
	guest.send("GET", "/login", nil)
	for _, method := range []string{"GET", "POST"} {
		if got := guest.send(method, "/bots/new", url.Values{"name": {"anonymous"}, "csrf_token": {guest.cookie("piko_csrf")}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Fatalf("anonymous creation %s: %d", method, got.Code)
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		if got := b.send("POST", "/bots/new", url.Values{"name": {"rejected"}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal("creation accepted invalid CSRF")
		}
	}
	if got := b.send("PUT", "/bots/new", url.Values{"csrf_token": {b.cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatal("creation accepted another method")
	}
	if got := b.send("GET", "/bots/new?name=not-saved", nil); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots", nil); !strings.Contains(page.Body.String(), "هنوز رباتی نساخته") {
		t.Fatal("GET or rejected mutation saved a Bot")
	}
	if got := b.post("/bots/new", url.Values{"name": {"خصوصی"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("unconnected-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1", "/bots/1/draft", preview, "/bots/1/submissions", "/bots/1/activate"} {
		if got := other.send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "خصوصی") {
			t.Fatalf("cross-owner read: %s %d", path, got.Code)
		}
	}
	posts := []string{"/bots/1/draft", "/bots/1/preview", preview + "/choose", preview + "/restart", "/bots/1/publish", "/bots/1/delete", "/bots/1/replace-token", "/bots/1/reconnect", "/bots/1/disconnect", "/bots/1/activate", "/bots/1/pause", "/bots/1/resume"}
	for _, path := range posts {
		v := url.Values{"token": {testBotToken}, "confirm_delete": {"yes"}, "operate": {"yes"}}
		if got := other.post(path, v); got.Code != 404 {
			t.Fatalf("cross-owner mutation: %s %d", path, got.Code)
		}
		if got := b.send("POST", path, v); got.Code != 403 {
			t.Fatalf("CSRF mutation: %s %d", path, got.Code)
		}
		v.Set("csrf_token", b.cookie("piko_csrf"))
		if got := b.send("PUT", path, v); got.Code != 405 {
			t.Fatalf("non-POST mutation: %s %d", path, got.Code)
		}
	}
	if page := other.send("GET", "/bots", nil); strings.Contains(page.Body.String(), "خصوصی") {
		t.Fatal("list exposed another owner's Bot")
	}
	if got := other.post("/bots/new", url.Values{"name": {"ربات دیگری"}}); got.Code != 303 || got.Header().Get("Location") != "/bots/2/draft" {
		t.Fatal("creation did not retain the signed-in owner")
	}
	if page := b.send("GET", "/bots/2", nil); page.Code != 404 {
		t.Fatal("first owner can access the second owner's Bot")
	}
}

func TestUnconnectedUpgradePreservesPopulatedBotsAndLifecycleStates(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست محفوظ", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پیشرفت محفوظ", 1)
	preview := startLegacyPreview(t, d.a, d.b)
	d.f.mu.Lock()
	d.f.identityID = 222222
	d.f.mu.Unlock()
	if got := d.b.post("/bots/connect", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/2/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := map[string]string{}
	for _, path := range []string{"/account", "/bots/1/draft", "/bots/1/submissions/1", preview, "/bots/2"} {
		before[path] = d.b.send("GET", path, nil).Body.String()
	}
	stop()
	legacy, err := database.Open(t.Context(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	rollbackToMigration(t, legacy, "000010_draft_revisions")
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
	for path, body := range before {
		if page := d.b.send("GET", path, nil); page.Code != 200 || page.Body.String() != body {
			t.Fatalf("upgrade altered retained data: %s", path)
		}
	}
	if got := d.b.post("/bots/new", url.Values{"name": {"ربات بدون اتصال"}}); got.Code != 303 || got.Header().Get("Location") != "/bots/3/draft" {
		t.Fatal("upgraded installation cannot create an Unconnected Bot")
	}
	for _, path := range []string{"/bots", "/dashboard"} {
		page := d.b.send("GET", path, nil).Body.String()
		for _, status := range []string{"هنوز متصل نشده", "اتصال قطع شده", "تأیید شده"} {
			if !strings.Contains(page, status) {
				t.Fatalf("distinct lifecycle state %q missing at %s", status, path)
			}
		}
	}
	if got := d.b.post("/bots/connect", url.Values{"token": {replacementToken}}); got.Code != 409 {
		t.Fatal("upgrade released a disconnected identity")
	}
	d.f.mu.Lock()
	d.f.identityID = 0
	d.f.mu.Unlock()
	if got := d.b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 409 {
		t.Fatal("upgrade released a connected identity")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if page := d.b.send("GET", "/bots/1/submissions/2", nil); !strings.Contains(page.Body.String(), "پیشرفت محفوظ") {
		t.Fatal("upgrade lost unfinished answers or published Flow")
	}
	if got := d.b.post("/bots/2/reconnect", url.Values{"token": {replacementToken}}); got.Code != 422 {
		t.Fatal("disconnected Bot allowed a different identity")
	}
	d.f.mu.Lock()
	d.f.identityID = 222222
	d.f.mu.Unlock()
	if got := d.b.post("/bots/2/reconnect", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal("retained identity cannot reconnect")
	}
}

func TestUnconnectedRollbackRefusesToDiscardWorkAndRestoresForeignKeys(t *testing.T) {
	a, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"کار محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	preview := startLegacyPreview(t, a, b)
	// Arrange retained work before removing columns used by today's server.
	// Exercise the Bot-table rebuild, independently of later migrations.
	rollbackToMigration(t, a.db, "000011_unconnected_bots")
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if err := database.Migrate(t.Context(), a.db, true); err == nil {
		t.Fatal("rollback discarded an Unconnected Bot")
	}
	if current := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(current, before) {
		t.Fatal("failed rollback changed saved work")
	}
	var foreignKeys int
	if err := a.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("failed migration left foreign keys disabled")
	}
	if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.send("GET", preview, nil); got.Code != 404 {
		t.Fatal("foreign-key cascades were not restored")
	}
	if err := database.Migrate(t.Context(), a.db, true); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/new", url.Values{"name": {"دوباره"}}); got.Code != 303 || got.Header().Get("Location") != "/bots/2/draft" {
		t.Fatal("rebuild lost the Bot ID high-water mark")
	}
}
