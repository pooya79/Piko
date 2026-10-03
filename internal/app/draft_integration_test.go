package app

import (
	"fmt"
	"github.com/pooya79/Piko/internal/platform/database"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const structuredDraft = `{"version":1,"welcome":{"id":"welcome","type":"message","text":"Hello"},"menu":{"id":"menu","type":"menu","text":"Choose","choices":[{"id":"hours","label":"Hours","target":"reply"}]},"messages":[{"id":"reply","type":"message","text":"Open 9 to 5"}]}`

func draftFixture(t *testing.T) (*App, *accountBrowser) {
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
	b.send(http.MethodGet, "/bots/connect", nil)
	if got := b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	connected.Store(true)
	return a, b
}

func welcomeDraft() url.Values {
	return url.Values{"welcome": {"سلام <دوست>"}, "menu_prompt": {"چه چیزی می\u200cخواهی؟"}, "choice_label": {"ساعت کار", "Contact"}, "choice_message": {"شنبه تا پنجشنبه، ۹ تا ۱۷", "Call 02112345678"}}
}

func TestOwnerSavesAndLoadsWelcomeMenuDraft(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); got.Code != 200 {
		t.Fatalf("configure: %d", got.Code)
	}
	if got := b.post("/bots/1/draft", welcomeDraft()); got.Code != 303 {
		t.Fatalf("save: %d", got.Code)
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	got := b.send(http.MethodGet, "/bots/1/draft", nil)
	for _, want := range []string{"سلام &lt;دوست&gt;", "چه چیزی می\u200cخواهی؟", "ساعت کار", "Call 02112345678"} {
		if got.Code != 200 || !strings.Contains(got.Body.String(), want) {
			t.Fatalf("saved Draft missing %q: %d", want, got.Code)
		}
	}
	if got := b.send(http.MethodGet, "/bots/1", nil); !strings.Contains(got.Body.String(), "فعال نشده") {
		t.Fatal("saving activated Bot")
	}
}

func TestPreviewRunsConfiguredMenuAndRestartsOnlyTestState(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", welcomeDraft()); got.Code != 303 {
		t.Fatalf("save: %d", got.Code)
	}
	started := b.post("/bots/1/preview", url.Values{})
	if started.Code != 303 {
		t.Fatalf("start Preview: %d", started.Code)
	}
	path := started.Header().Get("Location")
	if got := b.send(http.MethodGet, path, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "سلام &lt;دوست&gt;") || !strings.Contains(got.Body.String(), "ساعت کار") {
		t.Fatalf("initial Preview: %d", got.Code)
	}
	if got := b.post(path+"/choose", url.Values{"choice": {"1"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatalf("choice: %d", got.Code)
	}
	if got := b.send(http.MethodGet, path, nil); !strings.Contains(got.Body.String(), "شنبه تا پنجشنبه، ۹ تا ۱۷") {
		t.Fatal("Preview did not emit configured message")
	}
	if got := b.post(path+"/choose", url.Values{"choice": {"2"}, "revision": {"2"}}); got.Code != 303 {
		t.Fatalf("second choice: %d", got.Code)
	}
	if got := b.send(http.MethodGet, path, nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("Preview did not navigate back to menu")
	}
	if got := b.post(path+"/restart", url.Values{"revision": {"3"}}); got.Code != 303 {
		t.Fatalf("restart: %d", got.Code)
	}
	got := b.send(http.MethodGet, path, nil).Body.String()
	if strings.Contains(got, "Call 02112345678") || strings.Contains(got, "شنبه تا پنجشنبه، ۹ تا ۱۷") || !strings.Contains(got, "سلام &lt;دوست&gt;") {
		t.Fatal("restart did not clear test conversation")
	}
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("Preview restart changed saved Draft")
	}
	if got := b.send(http.MethodGet, "/bots/1", nil); !strings.Contains(got.Body.String(), "فعال نشده") {
		t.Fatal("Preview activated Telegram")
	}
}

func TestInvalidDraftDefinitionsPreserveSavedConfiguration(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", welcomeDraft()); got.Code != 303 {
		t.Fatal("initial save failed")
	}
	for _, tc := range []struct{ name, definition string }{
		{"unsupported version", strings.Replace(structuredDraft, `"version":1`, `"version":3`, 1)},
		{"script Block", strings.Replace(structuredDraft, `"type":"message"`, `"type":"script"`, 1)},
		{"unknown executable field", strings.Replace(structuredDraft, `"text":"Hello"`, `"text":"Hello","script":"alert(1)"`, 1)},
		{"missing destination", strings.Replace(structuredDraft, `"target":"reply"`, `"target":"missing"`, 1)},
		{"menu destination loop", strings.Replace(structuredDraft, `"target":"reply"`, `"target":"menu"`, 1)},
		{"welcome destination loop", strings.Replace(structuredDraft, `"target":"reply"`, `"target":"welcome"`, 1)},
		{"duplicate Block ID", strings.Replace(structuredDraft, `"id":"reply"`, `"id":"welcome"`, 1)},
		{"empty welcome", strings.Replace(structuredDraft, `"text":"Hello"`, `"text":" "`, 1)},
		{"trailing JSON", structuredDraft + ` {}`},
		{"malformed JSON", `{"version":1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := b.post("/bots/1/draft", url.Values{"definition": {tc.definition}})
			if got.Code != 422 || !strings.Contains(got.Body.String(), `role="alert"`) {
				t.Fatalf("invalid definition: %d", got.Code)
			}
			if saved := b.send(http.MethodGet, "/bots/1/draft", nil); !strings.Contains(saved.Body.String(), "Call 02112345678") {
				t.Fatal("invalid definition replaced saved Draft")
			}
		})
	}
}

func TestDraftSettingsValidateChoicesAndTextBounds(t *testing.T) {
	_, b := draftFixture(t)
	for _, tc := range []struct {
		name   string
		change func(url.Values)
	}{
		{"no choices", func(v url.Values) { v.Del("choice_label"); v.Del("choice_message") }},
		{"incomplete choice", func(v url.Values) { v["choice_message"][0] = "" }},
		{"duplicate labels", func(v url.Values) { v["choice_label"][1] = v["choice_label"][0] }},
		{"too many choices", func(v url.Values) {
			v["choice_label"] = []string{"1", "2", "3", "4", "5", "6", "7"}
			v["choice_message"] = []string{"a", "b", "c", "d", "e", "f", "g"}
		}},
		{"mismatched fields", func(v url.Values) { v["choice_message"] = []string{"a"} }},
		{"welcome too long", func(v url.Values) { v.Set("welcome", strings.Repeat("س", 2001)) }},
		{"menu too long", func(v url.Values) { v.Set("menu_prompt", strings.Repeat("س", 2001)) }},
		{"label too long", func(v url.Values) { v["choice_label"][0] = strings.Repeat("س", 81) }},
		{"message too long", func(v url.Values) { v["choice_message"][0] = strings.Repeat("س", 2001) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := welcomeDraft()
			tc.change(values)
			got := b.post("/bots/1/draft", values)
			if got.Code != 422 || !strings.Contains(got.Body.String(), `role="alert"`) {
				t.Fatalf("settings validation: %d", got.Code)
			}
		})
	}
	values := welcomeDraft()
	values.Set("welcome", strings.Repeat("س", 2000))
	values["choice_label"][0] = strings.Repeat("س", 80)
	values["choice_label"] = append(values["choice_label"], "")
	values["choice_message"] = append(values["choice_message"], "")
	if got := b.post("/bots/1/draft", values); got.Code != 303 {
		t.Fatalf("valid bounds and unused fields: %d", got.Code)
	}
}

func TestDraftTextBoundsRemainReadableAfterJSONEscaping(t *testing.T) {
	_, b := draftFixture(t)
	values := url.Values{"welcome": {strings.Repeat("<", 2000)}, "menu_prompt": {strings.Repeat("&", 2000)}, "choice_label": {"1", "2", "3", "4", "5", "6"}, "choice_message": {}}
	for range 6 {
		values["choice_message"] = append(values["choice_message"], strings.Repeat(">", 2000))
	}
	if got := b.post("/bots/1/draft", values); got.Code != 303 {
		t.Fatalf("valid text save: %d", got.Code)
	}
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), strings.Repeat("&lt;", 2000)) {
		t.Fatalf("escaped valid Draft cannot load: %d", got.Code)
	}
	if got := b.post("/bots/1/preview", url.Values{}); got.Code != 303 {
		t.Fatalf("escaped valid Draft cannot Preview: %d", got.Code)
	}
}

func TestStructuredDraftRunsThroughSamePreviewEngine(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", url.Values{"definition": {structuredDraft}}); got.Code != 303 {
		t.Fatalf("structured save: %d", got.Code)
	}
	started := b.post("/bots/1/preview", url.Values{})
	path := started.Header().Get("Location")
	if got := b.post(path+"/choose", url.Values{"choice": {"hours"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatalf("structured choice: %d", got.Code)
	}
	if got := b.send(http.MethodGet, path, nil); !strings.Contains(got.Body.String(), "Open 9 to 5") {
		t.Fatal("structured Blocks not executed")
	}
}

func TestPreviewInstancesAndDraftSnapshotsStayIsolatedAcrossRestart(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.post("/bots/1/draft", welcomeDraft()); got.Code != 303 {
		t.Fatal("save failed")
	}
	first := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	second := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if first == second || first == "" {
		t.Fatal("Preview instances share identity")
	}
	if got := b.post(first+"/choose", url.Values{"choice": {"2"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal("choice failed")
	}
	updated := welcomeDraft()
	updated.Set("welcome", "پیام تازه")
	updated["choice_message"][1] = "پاسخ تازه"
	if got := b.post("/bots/1/draft", updated); got.Code != 303 {
		t.Fatal("edit failed")
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if got := b.send(http.MethodGet, first, nil); !strings.Contains(got.Body.String(), "Call 02112345678") || strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("open Preview lost state or changed snapshot")
	}
	if got := b.send(http.MethodGet, second, nil); strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("Preview state leaked between instances")
	}
	if got := b.post(first+"/restart", url.Values{"revision": {"2"}}); got.Code != 303 {
		t.Fatal("restart failed")
	}
	if got := b.send(http.MethodGet, first, nil); strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("restart replaced snapshot with edited Draft")
	}
	third := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.send(http.MethodGet, third, nil); !strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("new Preview did not load latest saved Draft")
	}
}

func TestDraftAndPreviewRequireOwnerAuthenticationCSRFAndPOST(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.post("/bots/1/draft", welcomeDraft()); got.Code != 303 {
		t.Fatal("save failed")
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	visitor := newAccountBrowser(t, a.server.Handler)
	gets := []string{"/bots/1/draft", "/bots/1/preview", path}
	posts := []string{"/bots/1/draft", "/bots/1/preview", path + "/choose", path + "/restart"}
	for _, p := range gets {
		if got := visitor.send(http.MethodGet, p, nil); got.Code != 303 {
			t.Errorf("anonymous GET %s: %d", p, got.Code)
		}
	}
	visitor.send(http.MethodGet, "/register", nil)
	for _, p := range posts {
		if got := visitor.post(p, url.Values{}); got.Code != 303 {
			t.Errorf("anonymous POST %s: %d", p, got.Code)
		}
	}
	for _, p := range posts {
		for _, token := range []string{"", "invalid"} {
			if got := b.send(http.MethodPost, p, url.Values{"csrf_token": {token}}); got.Code != 403 {
				t.Errorf("CSRF %s: %d", p, got.Code)
			}
		}
		if got := b.send(http.MethodPut, p, url.Values{"csrf_token": {b.cookie("piko_csrf")}}); got.Code != 405 {
			t.Errorf("PUT %s: %d", p, got.Code)
		}
	}
	for _, p := range []string{path + "/choose", path + "/restart"} {
		if got := b.send(http.MethodGet, p, nil); got.Code != 405 {
			t.Errorf("GET mutation %s: %d", p, got.Code)
		}
	}
	visitor.send(http.MethodGet, "/register", nil)
	if got := visitor.post("/register", registerValues("preview-other@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal("second registration failed")
	}
	for _, p := range gets {
		if got := visitor.send(http.MethodGet, p, nil); got.Code != 404 || strings.Contains(got.Body.String(), "Draft Bot") {
			t.Errorf("other owner GET %s: %d", p, got.Code)
		}
	}
	for _, p := range posts {
		if got := visitor.post(p, welcomeDraft()); got.Code != 404 {
			t.Errorf("other owner POST %s: %d", p, got.Code)
		}
	}
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("unauthorized mutation changed Draft")
	}
}

func TestPreviewRejectsInvalidAndStaleChoicesWithoutChangingState(t *testing.T) {
	_, b := draftFixture(t)
	b.post("/bots/1/draft", welcomeDraft())
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.post(path+"/choose", url.Values{"choice": {"unknown"}, "revision": {"1"}}); got.Code != 422 {
		t.Fatalf("invalid choice: %d", got.Code)
	}
	if got := b.post(path+"/choose", url.Values{"choice": {"1"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal("valid choice failed")
	}
	for _, action := range []string{"choose", "restart"} {
		if got := b.post(path+"/"+action, url.Values{"choice": {"2"}, "revision": {"1"}}); got.Code != 409 {
			t.Fatalf("stale %s: %d", action, got.Code)
		}
	}
	body := b.send(http.MethodGet, path, nil).Body.String()
	if !strings.Contains(body, "شنبه تا پنجشنبه، ۹ تا ۱۷") || strings.Contains(body, "Call 02112345678") {
		t.Fatal("stale action changed conversation")
	}
}

func TestConcurrentPreviewChoicesAcceptOnlyOneRevision(t *testing.T) {
	_, b := draftFixture(t)
	b.post("/bots/1/draft", welcomeDraft())
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	start := make(chan struct{})
	statuses := make(chan int, 2)
	for _, choice := range []string{"1", "2"} {
		go func() {
			<-start
			statuses <- b.post(path+"/choose", url.Values{"choice": {choice}, "revision": {"1"}}).Code
		}()
	}
	close(start)
	first, second := <-statuses, <-statuses
	if !((first == 303 && second == 409) || (first == 409 && second == 303)) {
		t.Fatalf("concurrent statuses: %d %d", first, second)
	}
	body := b.send(http.MethodGet, path, nil).Body.String()
	hours, contact := strings.Contains(body, "شنبه تا پنجشنبه، ۹ تا ۱۷"), strings.Contains(body, "Call 02112345678")
	if hours == contact {
		t.Fatal("concurrent choices did not preserve exactly one reply")
	}
}

func TestPreviewRequiresSavedDraftAndExpiresWithoutChangingDraft(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.post("/bots/1/preview", url.Values{}); got.Code != 409 {
		t.Fatalf("unsaved Preview: %d", got.Code)
	}
	b.post("/bots/1/draft", welcomeDraft())
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	// Advance time at the storage boundary; assertions use the HTTP contract.
	if _, err := a.db.Exec("UPDATE bot_previews SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if got := b.send(http.MethodGet, path, nil); got.Code != 404 {
		t.Fatalf("expired Preview: %d", got.Code)
	}
	if got := b.post(path+"/restart", url.Values{"revision": {"1"}}); got.Code != 404 {
		t.Fatalf("expired restart: %d", got.Code)
	}
	if err := a.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("expiration removed Draft")
	}
}

func TestDraftAndPreviewStorageFailuresPreserveSavedState(t *testing.T) {
	a, b := draftFixture(t)
	b.post("/bots/1/draft", welcomeDraft())
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	for _, tc := range []struct {
		table, path string
		values      url.Values
	}{
		{"bot_drafts", "/bots/1/draft", url.Values{"definition": {structuredDraft}}},
		{"bot_previews", path + "/choose", url.Values{"choice": {"2"}, "revision": {"1"}}},
	} {
		if _, err := a.db.Exec("CREATE TRIGGER fail_update BEFORE UPDATE ON " + tc.table + " BEGIN SELECT RAISE(ABORT, 'private-configuration'); END"); err != nil {
			t.Fatal(err)
		}
		got := b.post(tc.path, tc.values)
		if got.Code != 500 || strings.Contains(got.Body.String(), "private-configuration") {
			t.Fatalf("storage failure %s: %d", tc.table, got.Code)
		}
		if _, err := a.db.Exec("DROP TRIGGER fail_update"); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("failed save changed Draft")
	}
	if got := b.send(http.MethodGet, path, nil); strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("failed Preview update changed conversation")
	}
}

func TestDraftMigrationPreservesExistingBotAndOwnerSession(t *testing.T) {
	a, b := draftFixture(t)
	for range 4 {
		if err := database.Migrate(t.Context(), a.db, true); err != nil {
			t.Fatal(err)
		}
	}
	var bots, sessions int
	if err := a.db.QueryRow("SELECT count(*) FROM bots").Scan(&bots); err != nil || bots != 1 {
		t.Fatal("rollback lost existing Bot")
	}
	if err := a.db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatal("rollback lost owner session")
	}
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/draft", welcomeDraft()); got.Code != 303 {
		t.Fatalf("forward migration: %d", got.Code)
	}
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
	if got := b.send(http.MethodGet, "/bots/1/draft", nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("migration reapplication lost Draft")
	}
}
