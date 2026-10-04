package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
)

func TestBuilderDeletedBotCannotFinishLateRun(t *testing.T) {
	started, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			close(cancelled)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"late reply"},"finish_reason":"stop"}]}`))
	})
	defer close(release)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code, got.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("deleted Bot still has live provider work")
	}
	// Accounting intentionally outlives the deleted Bot and is observable from
	// another chat, even though the original history is no longer accessible.
	if got := b.post("/bots/new", url.Values{"name": {"ربات دوم"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/2/chats", url.Values{"title": {"گفتگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := b.send("GET", "/bots/2/chats/3", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-admitted="1"`) {
		t.Fatal("deletion lost daily accounting")
	}
	if got := b.send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("deleted history returned")
	}
}

func TestBuilderClientDisconnectKeepsAdmittedWorkAndStatusReadsDoNotReplay(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ پس از قطع اتصال"},"finish_reason":"stop"}]}`))
	})
	defer close(release)
	ctx, disconnect := context.WithCancel(t.Context())
	values := url.Values{"message": {"درخواست ماندگار"}, "csrf_token": {b.cookie("piko_csrf")}}
	req := httptest.NewRequest("POST", "/bots/1/chats/1/messages", strings.NewReader(values.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range b.jar.Cookies(b.base) {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(response, req)
	if response.Code != 303 {
		t.Fatal("admission did not return before provider completion", response.Code)
	}
	disconnect()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	for range 3 {
		if page := b.send("GET", "/bots/1/chats/1", nil); !strings.Contains(page.Body.String(), `data-run-status="running"`) || !strings.Contains(page.Body.String(), `data-admitted="1"`) {
			t.Fatal("disconnect cancelled work or reconnect consumed allowance")
		}
		status := b.send("GET", "/bots/1/chats/1/status", nil)
		if status.Code != 200 || status.Body.String() != "{\"id\":1,\"status\":\"running\"}\n" || status.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("owner cannot recover live status", status.Code, status.Body.String())
		}
	}
	release <- struct{}{}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, "پاسخ پس از قطع اتصال") || calls.Load() != 1 {
		t.Fatal("saved reply missing or reconnect replayed provider")
	}
}

func TestBuilderInterruptedRequestRequiresExplicitOwnerRetry(t *testing.T) {
	started := make(chan struct{})
	requests := make(chan string, 2)
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ تلاش دوباره"},"finish_reason":"stop"}]}`))
	})
	stop := startBuilderApp(t, a)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست اصلی"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	stop()
	<-requests
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	page := waitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	path := "/bots/1/chats/1/runs/1/retry"
	if !strings.Contains(page, `action="`+path+`"`) || calls.Load() != 1 {
		t.Fatal("interrupted request has no explicit retry or was replayed")
	}
	if got := b.send("GET", path, nil); got.Code != 405 {
		t.Fatal("retry allowed without POST")
	}
	if got := b.send("POST", path, url.Values{"csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("retry allowed without CSRF")
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("retry-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := other.post(path, url.Values{}); got.Code != 404 {
		t.Fatal("another owner retried request")
	}
	if got := other.send("GET", "/bots/1/chats/1/status", nil); got.Code != 404 {
		t.Fatal("another owner read status")
	}
	if got := b.post(path, url.Values{}); got.Code != 303 {
		t.Fatal("explicit retry rejected", got.Code)
	}
	page = waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(<-requests, "درخواست اصلی") || !strings.Contains(page, "پاسخ تلاش دوباره") || !strings.Contains(page, `data-admitted="2"`) {
		t.Fatal("retry did not reuse saved request and normal admission")
	}
	if got := b.post(path, url.Values{}); got.Code != 409 || calls.Load() != 2 {
		t.Fatal("stale retry replayed completed work")
	}
}

func TestBuilderBackgroundMigrationRetainsSavedOutcomeHistoryAndDraft(t *testing.T) {
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ محفوظ"},"finish_reason":"stop"}],"usage":{"total_tokens":19,"cost":0.001}}`))
	})
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"پیام محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	a.stopRequests()
	a.builder.Wait()
	// Exercise a populated pre-000014 database, then apply the forward upgrade.
	rollbackToMigration(t, a.db, "000013_builder_runs")
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
	_ = a.db.Close()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if after := b.send("GET", "/bots/1/chats/1", nil); after.Code != 200 || before != after.Body.String() {
		t.Fatal("upgrade lost account/session/Bot/history/outcome/accounting")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); after.Encode() != draft.Encode() || calls.Load() != 1 {
		t.Fatal("upgrade changed saved Draft or replayed provider")
	}
}
