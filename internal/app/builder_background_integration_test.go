package app

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

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
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست اصلی"}}); got.Code != 303 {
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
	b.Router = restarted.server.Handler
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	path := "/bots/1/chats/1/runs/1/retry"
	if !strings.Contains(page, `action="`+path+`"`) || calls.Load() != 1 {
		t.Fatal("interrupted request has no explicit retry or was replayed")
	}
	if got := b.Send("GET", path, nil); got.Code != 405 {
		t.Fatal("retry allowed without POST")
	}
	if got := b.Send("POST", path, url.Values{"csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("retry allowed without CSRF")
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("retry-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := other.Post(path, url.Values{}); got.Code != 404 {
		t.Fatal("another owner retried request")
	}
	if got := other.Send("GET", "/bots/1/chats/1/status", nil); got.Code != 404 {
		t.Fatal("another owner read status")
	}
	if got := b.Post(path, url.Values{}); got.Code != 303 {
		t.Fatal("explicit retry rejected", got.Code)
	}
	page = fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(<-requests, "درخواست اصلی") || !strings.Contains(page, "پاسخ تلاش دوباره") || !strings.Contains(page, `data-admitted="2"`) {
		t.Fatal("retry did not reuse saved request and normal admission")
	}
	if got := b.Post(path, url.Values{}); got.Code != 409 || calls.Load() != 2 {
		t.Fatal("stale retry replayed completed work")
	}
}
