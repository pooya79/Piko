package builder_test

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
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderDeletedBotCannotFinishLateRun(t *testing.T) {
	started, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
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
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	if got := b.Post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code, got.Body.String())
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("deleted Bot still has live provider work")
	}
	// Accounting intentionally outlives the deleted Bot and is observable from
	// another chat, even though the original history is no longer accessible.
	if got := b.Post("/bots/new", url.Values{"name": {"ربات دوم"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/2/chats", url.Values{"title": {"گفتگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := b.Send("GET", "/bots/2/chats/3", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-admitted="1"`) {
		t.Fatal("deletion lost daily accounting")
	}
	if got := b.Send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("deleted history returned")
	}
}

func TestBuilderClientDisconnectKeepsAdmittedWorkAndStatusReadsDoNotReplay(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
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
	values := url.Values{"message": {"درخواست ماندگار"}, "csrf_token": {b.Cookie("piko_csrf")}}
	req := httptest.NewRequest("POST", "/bots/1/chats/1/messages", strings.NewReader(values.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range b.Jar.Cookies(b.Base) {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	a.Handler.ServeHTTP(response, req)
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
		if page := b.Send("GET", "/bots/1/chats/1", nil); !strings.Contains(page.Body.String(), `data-run-status="running"`) || !strings.Contains(page.Body.String(), `data-admitted="1"`) {
			t.Fatal("disconnect cancelled work or reconnect consumed allowance")
		}
		status := b.Send("GET", "/bots/1/chats/1/status", nil)
		if status.Code != 200 || status.Body.String() != "{\"id\":1,\"status\":\"running\"}\n" || status.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("owner cannot recover live status", status.Code, status.Body.String())
		}
	}
	release <- struct{}{}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, "پاسخ پس از قطع اتصال") || calls.Load() != 1 {
		t.Fatal("saved reply missing or reconnect replayed provider")
	}
}

func TestBuilderBackgroundMigrationRetainsSavedOutcomeHistoryAndDraft(t *testing.T) {
	var calls atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ محفوظ"},"finish_reason":"stop"}],"usage":{"total_tokens":19,"cost":0.001}}`))
	})
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"پیام محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	draft := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	a.StopWork()
	a.Builder.Wait()
	// Exercise a populated pre-000014 database, then apply the forward upgrade.
	fixture.RollbackToMigration(t, a.DB, "000013_builder_runs")
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	_ = a.DB.Close()
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	if after := b.Send("GET", "/bots/1/chats/1", nil); after.Code != 200 || before != after.Body.String() {
		t.Fatal("upgrade lost account/session/Bot/history/outcome/accounting")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()); after.Encode() != draft.Encode() || calls.Load() != 1 {
		t.Fatal("upgrade changed saved Draft or replayed provider")
	}
}
