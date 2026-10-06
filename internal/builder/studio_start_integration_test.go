package builder_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func assertStartCounts(t *testing.T, db *sql.DB, chats, messages, runs int) {
	t.Helper()
	for _, check := range []struct {
		table string
		want  int
	}{{"builder_chats", chats}, {"builder_messages", messages}, {"builder_runs", runs}} {
		var got int
		if err := db.QueryRow("SELECT count(*) FROM " + check.table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s: got %d, want %d", check.table, got, check.want)
		}
	}
}

func TestBotStudioFirstMessageSavesNamedChatAndReplaySurvivesRestart(t *testing.T) {
	var calls atomic.Int64
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fixture.MemoryReply(w, "پاسخ نخست")
	})
	b.Post("/bots/new", url.Values{"name": {"استودیو"}})
	for range 2 {
		got := fixture.StudioRequest(b, "GET", "/bots/1/studio", nil)
		body := got.Body.String()
		for _, want := range []string{`data-studio-unsaved`, `data-chat-url="/bots/1/studio"`, `action="/bots/1/chats"`, `data-studio-preview`, `data-studio-flow`, `name="request_key"`} {
			if got.Code != 200 || !strings.Contains(body, want) {
				t.Fatalf("fresh studio missing %s: %d", want, got.Code)
			}
		}
		if strings.Contains(body, `name="title"`) || strings.Contains(body, `aria-current="page" class="piko-saved-chat"`) {
			t.Fatal("fresh studio requires a title or selects saved work")
		}
	}
	assertStartCounts(t, a.DB, 0, 0, 0)
	if got := b.Send("GET", "/bots/1/chats", nil); got.Code != 303 || got.Header().Get("Location") != "/bots/1/studio" {
		t.Fatal("old list URL does not redirect to studio")
	}
	values := url.Values{"message": {"  منوی <کافه>\nرا تغییر بده  "}, "request_key": {"first-bot-message"}}
	first := fixture.StudioRequest(b, "POST", "/bots/1/chats", values)
	if first.Code != 200 || first.Header().Get("X-Piko-Accepted") != "true" || !strings.Contains(first.Body.String(), `data-chat-url="/bots/1/chats/1"`) || strings.Contains(first.Body.String(), `data-studio-unsaved`) {
		t.Fatalf("first message did not become a saved chat: %d", first.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	var title string
	if err := a.DB.QueryRow("SELECT title FROM builder_chats WHERE id=1").Scan(&title); err != nil || title != "منوی <کافه> را تغییر بده" {
		t.Fatalf("first-message title: %q %v", title, err)
	}
	if got := b.Send("GET", "/bots/1/studio", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `data-studio-unsaved`) || !strings.Contains(got.Body.String(), `href="/bots/1/chats/1"`) || !strings.Contains(got.Body.String(), "منوی &lt;کافه&gt; را تغییر بده") {
		t.Fatal("re-entering studio did not open a fresh composer with escaped saved history")
	}
	assertStartCounts(t, a.DB, 1, 2, 1)
	a.StopWork()
	a.Builder.Wait()
	_ = a.DB.Close()
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	replay := b.Post("/bots/1/chats", values)
	if replay.Code != 303 || replay.Header().Get("Location") != "/bots/1/chats/1" {
		t.Fatal("lost first-response replay created another conversation")
	}
	values.Set("message", "پیام متفاوت")
	if got := b.Post("/bots/1/chats", values); got.Code != 422 || !strings.Contains(got.Body.String(), `data-studio-unsaved`) {
		t.Fatal("start key accepted a different message")
	}
	assertStartCounts(t, restarted.DB, 1, 2, 1)
	if calls.Load() != 1 {
		t.Fatal("first submission replayed provider work")
	}
}

func TestBotStudioRejectedFirstMessageSavesNoChat(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values url.Values
		status int
	}{
		{"title only", url.Values{"title": {"empty chat"}}, 422},
		{"empty", url.Values{"message": {" "}, "request_key": {"start"}}, 422},
		{"duplicate message", url.Values{"message": {"one", "two"}, "request_key": {"start"}}, 422},
		{"missing key", url.Values{"message": {"پیام"}}, 422},
		{"empty key", url.Values{"message": {"پیام"}, "request_key": {" "}}, 422},
		{"duplicate key", url.Values{"message": {"پیام"}, "request_key": {"one", "two"}}, 422},
		{"long key", url.Values{"message": {"پیام"}, "request_key": {strings.Repeat("x", 129)}}, 422},
		{"bad revision", url.Values{"message": {"پیام"}, "request_key": {"start"}, "selected_revision": {"invalid"}}, 422},
		{"stale block", url.Values{"message": {"پیام"}, "request_key": {"start"}, "selected_block": {"missing"}, "selected_revision": {"99"}}, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) { t.Error("invalid first message invoked provider") })
			b.Post("/bots/new", url.Values{"name": {"ربات"}})
			got := fixture.StudioRequest(b, "POST", "/bots/1/chats", tc.values)
			if got.Code != tc.status || !strings.Contains(got.Body.String(), `data-studio-unsaved`) {
				t.Fatalf("invalid first message: %d", got.Code)
			}
			assertStartCounts(t, a.DB, 0, 0, 0)
		})
	}
	t.Run("unavailable", func(t *testing.T) {
		a, b := fixture.UnconnectedFixture(t)
		b.Post("/bots/new", url.Values{"name": {"ربات"}})
		if got := b.Post("/bots/1/chats", url.Values{"message": {"پیام محفوظ"}, "request_key": {"disabled"}}); got.Code != 503 || !strings.Contains(got.Body.String(), "پیام محفوظ") {
			t.Fatal("unavailable admission lost editable message")
		}
		assertStartCounts(t, a.DB, 0, 0, 0)
	})
	t.Run("storage failure", func(t *testing.T) {
		a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) { t.Error("rolled-back admission invoked provider") })
		b.Post("/bots/new", url.Values{"name": {"ربات"}})
		if _, err := a.DB.Exec(`CREATE TRIGGER fail_first_run BEFORE INSERT ON builder_runs BEGIN SELECT RAISE(ABORT,'admission failure'); END`); err != nil {
			t.Fatal(err)
		}
		if got := b.Post("/bots/1/chats", url.Values{"message": {"پیام"}, "request_key": {"failed"}}); got.Code != 500 {
			t.Fatal("storage failure accepted first turn", got.Code)
		}
		assertStartCounts(t, a.DB, 0, 0, 0)
	})
}

func TestBotStudioFirstMessageRequiresOwnerPOSTAndCSRF(t *testing.T) {
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) { t.Error("rejected first message invoked provider") })
	b.Post("/bots/new", url.Values{"name": {"خصوصی"}})
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("studio-start-other@example.test", "دیگر", "OwnerPassword123"))
	if got := other.Post("/bots/1/chats", url.Values{"message": {"پیام"}, "request_key": {"start"}}); got.Code != 404 {
		t.Fatal("cross-owner start accepted")
	}
	if got := other.Send("GET", "/bots/1/studio", nil); got.Code != 404 {
		t.Fatal("fresh studio leaked Bot")
	}
	if got := b.Send("POST", "/bots/1/chats", url.Values{"message": {"پیام"}, "request_key": {"start"}, "csrf_token": {"invalid"}}); got.Code != 403 {
		t.Fatal("first message bypassed CSRF")
	}
	if got := b.Send("PUT", "/bots/1/chats", url.Values{"csrf_token": {b.Cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatal("non-POST start accepted")
	}
	assertStartCounts(t, a.DB, 0, 0, 0)
}

func TestBotStudioConcurrentFirstMessagesAndAllowanceLeaveNoEmptyChats(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{DailyRequests: 1}, "error", func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fixture.MemoryReply(w, "پاسخ")
	})
	defer close(release)
	b.Post("/bots/new", url.Values{"name": {"ربات"}})
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, key := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- b.Post("/bots/1/chats", url.Values{"message": {"پیام"}, "request_key": {key}}).Code
		}()
	}
	wg.Wait()
	counts := map[int]int{}
	counts[<-results]++
	counts[<-results]++
	if counts[303] != 1 || counts[409] != 1 {
		t.Fatal("first-message starts bypassed Bot fence", counts)
	}
	<-started
	assertStartCounts(t, a.DB, 1, 1, 1)
	if got := b.Post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "stopped")
	if got := b.Post("/bots/1/chats", url.Values{"message": {"بیش از سهمیه"}, "request_key": {"third"}}); got.Code != 429 || !strings.Contains(got.Body.String(), `data-studio-unsaved`) {
		t.Fatal("new chat bypassed allowance", got.Code)
	}
	assertStartCounts(t, a.DB, 1, 2, 1)
}

func TestBotStudioFirstMessageKeysAreScopedAndTitlesBounded(t *testing.T) {
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) { fixture.MemoryReply(w, "پاسخ") })
	for _, name := range []string{"اول", "دوم"} {
		b.Post("/bots/new", url.Values{"name": {name}})
	}
	message := strings.Repeat("س", 81)
	for _, path := range []string{"/bots/1/chats", "/bots/2/chats", "/chats"} {
		got := b.Post(path, url.Values{"message": {message}, "request_key": {"same-key"}})
		if got.Code != 303 {
			t.Fatal("independent start key collided", path, got.Code)
		}
		fixture.WaitBuilder(t, b, got.Header().Get("Location"), "succeeded")
	}
	assertStartCounts(t, a.DB, 3, 6, 3)
	var title string
	if err := a.DB.QueryRow("SELECT title FROM builder_chats WHERE id=1").Scan(&title); err != nil || title != strings.Repeat("س", 79)+"…" {
		t.Fatalf("title exceeds Unicode bound: %q %v", title, err)
	}
}
