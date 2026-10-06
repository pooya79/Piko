package bot_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestPreviewRunsConfiguredMenuAndRestartsOnlyTestState(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	started := b.Post("/bots/1/preview", url.Values{})
	if started.Code != 303 {
		t.Fatalf("start Preview: %d", started.Code)
	}
	path := started.Header().Get("Location")
	if got := b.Send(http.MethodGet, path, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "سلام &lt;دوست&gt;") || !strings.Contains(got.Body.String(), "ساعت کار") {
		t.Fatalf("initial Preview: %d", got.Code)
	}
	if got := b.Post(path+"/choose", url.Values{"choice": {"1"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatalf("choice: %d", got.Code)
	}
	if got := b.Send(http.MethodGet, path, nil); !strings.Contains(got.Body.String(), "شنبه تا پنجشنبه، ۹ تا ۱۷") {
		t.Fatal("Preview did not emit configured message")
	}
	if got := b.Post(path+"/choose", url.Values{"choice": {"2"}, "revision": {"2"}}); got.Code != 303 {
		t.Fatalf("second choice: %d", got.Code)
	}
	if got := b.Send(http.MethodGet, path, nil); !strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("Preview did not navigate back to menu")
	}
	if got := b.Post(path+"/restart", url.Values{"revision": {"3"}}); got.Code != 303 {
		t.Fatalf("restart: %d", got.Code)
	}
	got := b.Send(http.MethodGet, path, nil).Body.String()
	if strings.Contains(got, "Call 02112345678") || strings.Contains(got, "شنبه تا پنجشنبه، ۹ تا ۱۷") || !strings.Contains(got, "سلام &lt;دوست&gt;") {
		t.Fatal("restart did not clear test conversation")
	}
	if !strings.Contains(b.DraftText(t, 1), "Call 02112345678") {
		t.Fatal("Preview restart changed saved Draft")
	}
	if got := b.Send(http.MethodGet, "/bots/1", nil); !strings.Contains(got.Body.String(), "فعال نشده") {
		t.Fatal("Preview activated Telegram")
	}
}

func TestStructuredDraftRunsThroughSamePreviewEngine(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, url.Values{"definition": {fixture.StructuredDraft}}); err != nil {
		t.Fatal(err)
	}
	started := b.Post("/bots/1/preview", url.Values{})
	path := started.Header().Get("Location")
	if got := b.Post(path+"/choose", url.Values{"choice": {"hours"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatalf("structured choice: %d", got.Code)
	}
	if got := b.Send(http.MethodGet, path, nil); !strings.Contains(got.Body.String(), "Open 9 to 5") {
		t.Fatal("structured Blocks not executed")
	}
}

func TestPreviewInstancesAndDraftSnapshotsStayIsolatedAcrossRestart(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	first := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	second := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if first == second || first == "" {
		t.Fatal("Preview instances share identity")
	}
	if got := b.Post(first+"/choose", url.Values{"choice": {"2"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal("choice failed")
	}
	updated := fixture.WelcomeDraft()
	updated.Set("welcome", "پیام تازه")
	updated["choice_message"][1] = "پاسخ تازه"
	if err := b.SaveDraft(t, 1, updated); err != nil {
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
	if got := b.Send(http.MethodGet, first, nil); !strings.Contains(got.Body.String(), "Call 02112345678") || strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("open Preview lost state or changed snapshot")
	}
	if got := b.Send(http.MethodGet, second, nil); strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("Preview state leaked between instances")
	}
	if got := b.Post(first+"/restart", url.Values{"revision": {"2"}}); got.Code != 303 {
		t.Fatal("restart failed")
	}
	if got := b.Send(http.MethodGet, first, nil); strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("restart replaced snapshot with edited Draft")
	}
	third := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.Send(http.MethodGet, third, nil); !strings.Contains(got.Body.String(), "پیام تازه") {
		t.Fatal("new Preview did not load latest saved Draft")
	}
}

func TestDraftAndPreviewRequireOwnerAuthenticationCSRFAndPOST(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	visitor := fixture.NewAccountBrowser(t, a.Handler)
	gets := []string{"/bots/1/preview", path}
	posts := []string{"/bots/1/preview", path + "/choose", path + "/restart"}
	for _, p := range gets {
		if got := visitor.Send(http.MethodGet, p, nil); got.Code != 303 {
			t.Errorf("anonymous GET %s: %d", p, got.Code)
		}
	}
	visitor.Send(http.MethodGet, "/register", nil)
	for _, p := range posts {
		if got := visitor.Post(p, url.Values{}); got.Code != 303 {
			t.Errorf("anonymous POST %s: %d", p, got.Code)
		}
	}
	for _, p := range posts {
		for _, token := range []string{"", "invalid"} {
			if got := b.Send(http.MethodPost, p, url.Values{"csrf_token": {token}}); got.Code != 403 {
				t.Errorf("CSRF %s: %d", p, got.Code)
			}
		}
		if got := b.Send(http.MethodPut, p, url.Values{"csrf_token": {b.Cookie("piko_csrf")}}); got.Code != 405 {
			t.Errorf("PUT %s: %d", p, got.Code)
		}
	}
	for _, p := range []string{path + "/choose", path + "/restart"} {
		if got := b.Send(http.MethodGet, p, nil); got.Code != 405 {
			t.Errorf("GET mutation %s: %d", p, got.Code)
		}
	}
	visitor.Send(http.MethodGet, "/register", nil)
	if got := visitor.Post("/register", fixture.RegisterValues("preview-other@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal("second registration failed")
	}
	for _, p := range gets {
		if got := visitor.Send(http.MethodGet, p, nil); got.Code != 404 || strings.Contains(got.Body.String(), "Draft Bot") {
			t.Errorf("other owner GET %s: %d", p, got.Code)
		}
	}
	for _, p := range posts {
		if got := visitor.Post(p, fixture.WelcomeDraft()); got.Code != 404 {
			t.Errorf("other owner POST %s: %d", p, got.Code)
		}
	}
	if !strings.Contains(b.DraftText(t, 1), "Call 02112345678") {
		t.Fatal("unauthorized mutation changed Draft")
	}
}

func TestPreviewRejectsInvalidAndStaleChoicesWithoutChangingState(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.Post(path+"/choose", url.Values{"choice": {"unknown"}, "revision": {"1"}}); got.Code != 422 {
		t.Fatalf("invalid choice: %d", got.Code)
	}
	if got := b.Post(path+"/choose", url.Values{"choice": {"1"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal("valid choice failed")
	}
	for _, action := range []string{"choose", "restart"} {
		if got := b.Post(path+"/"+action, url.Values{"choice": {"2"}, "revision": {"1"}}); got.Code != 409 {
			t.Fatalf("stale %s: %d", action, got.Code)
		}
	}
	body := b.Send(http.MethodGet, path, nil).Body.String()
	if !strings.Contains(body, "شنبه تا پنجشنبه، ۹ تا ۱۷") || strings.Contains(body, "Call 02112345678") {
		t.Fatal("stale action changed conversation")
	}
}

func TestConcurrentPreviewChoicesAcceptOnlyOneRevision(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	start := make(chan struct{})
	statuses := make(chan int, 2)
	for _, choice := range []string{"1", "2"} {
		go func() {
			<-start
			statuses <- b.Post(path+"/choose", url.Values{"choice": {choice}, "revision": {"1"}}).Code
		}()
	}
	close(start)
	first, second := <-statuses, <-statuses
	if !((first == 303 && second == 409) || (first == 409 && second == 303)) {
		t.Fatalf("concurrent statuses: %d %d", first, second)
	}
	body := b.Send(http.MethodGet, path, nil).Body.String()
	hours, contact := strings.Contains(body, "شنبه تا پنجشنبه، ۹ تا ۱۷"), strings.Contains(body, "Call 02112345678")
	if hours == contact {
		t.Fatal("concurrent choices did not preserve exactly one reply")
	}
}

func TestDraftAndPreviewStorageFailuresPreserveSavedState(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	for _, tc := range []struct {
		table, path string
		values      url.Values
	}{
		{"bot_previews", path + "/choose", url.Values{"choice": {"2"}, "revision": {"1"}}},
	} {
		if _, err := a.DB.Exec("CREATE TRIGGER fail_update BEFORE UPDATE ON " + tc.table + " BEGIN SELECT RAISE(ABORT, 'private-configuration'); END"); err != nil {
			t.Fatal(err)
		}
		got := b.Post(tc.path, tc.values)
		if got.Code != 500 || strings.Contains(got.Body.String(), "private-configuration") {
			t.Fatalf("storage failure %s: %d", tc.table, got.Code)
		}
		if _, err := a.DB.Exec("DROP TRIGGER fail_update"); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(b.DraftText(t, 1), "Call 02112345678") {
		t.Fatal("failed save changed Draft")
	}
	if got := b.Send(http.MethodGet, path, nil); strings.Contains(got.Body.String(), "Call 02112345678") {
		t.Fatal("failed Preview update changed conversation")
	}
}

func TestDraftMigrationPreservesExistingBotAndOwnerSession(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	fixture.RollbackToMigration(t, a.DB, "000004_bots")
	var bots, sessions int
	if err := a.DB.QueryRow("SELECT count(*) FROM bots").Scan(&bots); err != nil || bots != 1 {
		t.Fatal("rollback lost existing Bot")
	}
	if err := a.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatal("rollback lost owner session")
	}
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.DraftText(t, 1), "Call 02112345678") {
		t.Fatal("migration reapplication lost Draft")
	}
}
