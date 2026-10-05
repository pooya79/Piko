package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
)

func startPikoChat(t *testing.T, b *accountBrowser) string {
	t.Helper()
	got := b.post("/chats", url.Values{})
	if got.Code != 303 {
		t.Fatalf("start Piko chat: %d", got.Code)
	}
	return got.Header().Get("Location")
}

func TestPikoChatUnavailableAndTimeoutKeepHonestSavedState(t *testing.T) {
	_, disabled := unconnectedFixture(t)
	path := startPikoChat(t, disabled)
	page := disabled.send("GET", path, nil)
	if page.Code != 200 || strings.Contains(page.Body.String(), `action="`+path+`/messages"`) || !strings.Contains(page.Body.String(), "disabled") {
		t.Fatal("unavailable generation offers enabled composer")
	}
	if got := disabled.post(path+"/messages", url.Values{"message": {"پرسش"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-admitted="0"`) {
		t.Fatal("unavailable generation admitted work")
	}
	_, b := generalBuilderFixture(t, builder.Config{RunTimeout: 100 * time.Millisecond}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	path = startPikoChat(t, b)
	for range 2 {
		if got := b.post(path+"/messages", url.Values{"message": {"پرسش کند"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := waitBuilder(t, b, path, "timeout")
		if !strings.Contains(page, "پرسش کند") || strings.Contains(page, `data-after-revision=`) {
			t.Fatal("timeout lost question or applied a Draft")
		}
	}
}

func TestPikoChatPrivateSummariesAndExistingBuilderDataSurviveForwardMigration(t *testing.T) {
	requests := make(chan string, 32)
	var summaries atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			memoryReply(w, "یادآوری خصوصی اول")
			return
		}
		memoryReply(w, "پاسخ محفوظ")
	})
	// Exercise populated pre-upgrade chats, including a durable summary and paid runs.
	for turn := range 8 {
		if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {fmt.Sprintf("پیام پیشین %d", turn)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
	before := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	rollbackToMigration(t, a.db, "000016_builder_memory")
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
	if after := b.send("GET", "/bots/1/chats/1", nil); after.Code != 200 || after.Body.String() != before {
		t.Fatal("migration changed existing history, runs, session or accounting")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != draft {
		t.Fatal("migration changed existing Draft")
	}
	for len(requests) > 0 {
		<-requests
	}
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه پس از مهاجرت"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if request := <-requests; !strings.Contains(request, "یادآوری خصوصی اول") {
		t.Fatal("migration lost existing summary")
	}
	path := startPikoChat(t, b)
	for turn := range 8 {
		if got := b.post(path+"/messages", url.Values{"message": {fmt.Sprintf("پرسش خصوصی %d", turn)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := waitBuilder(t, b, path, "succeeded")
		if !strings.Contains(page, "پرسش خصوصی 0") {
			t.Fatal("summary removed displayed history")
		}
		for len(requests) > 0 {
			request := <-requests
			if strings.Contains(request, "پیام پیشین") || strings.Contains(request, "ادامه پس از مهاجرت") {
				t.Fatal("Bot history leaked into general memory")
			}
			if turn == 7 && !strings.Contains(request, "Summarize older messages") && !strings.Contains(request, "یادآوری خصوصی اول") {
				t.Fatal("general summary not used")
			}
		}
	}
	if summaries.Load() != 2 {
		t.Fatal("general and Bot chats did not each summarize")
	}
	if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.send("GET", path, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "پرسش خصوصی 0") {
		t.Fatal("Bot deletion removed unrelated general chat")
	}
	if got := b.send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("Bot deletion retained associated history")
	}
}

func TestPikoQuestionsSaveIndependentConversationWithoutBot(t *testing.T) {
	requests := make(chan string, 3)
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if tools, ok := body["tools"].([]any); ok && len(tools) != 0 {
			t.Error("general question received mutation tools")
		}
		encoded, _ := json.Marshal(body)
		requests <- string(encoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پیکو فرم درخواست رزرو می\u200cسازد؛ منظورتان آشنایی با امکانات است؟"},"finish_reason":"stop"}],"usage":{"total_tokens":7,"cost":0.001}}`))
	})
	if got := b.send("GET", "/builder", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `action="/chats"`) {
		t.Fatal("Piko has no independent conversation entry")
	}
	for _, turn := range []struct{ path, message string }{
		{"/chats/1", "حافظه نخست: چه قالب\u200cهایی دارید؟"},
		{"/chats/2", "حافظه دوم: رزرو شاید"},
		{"/chats/1", "ادامه نخست"},
	} {
		if turn.message != "ادامه نخست" {
			got := b.post("/chats", url.Values{})
			if got.Code != 303 || got.Header().Get("Location") != turn.path {
				t.Fatalf("start: %d %s", got.Code, got.Header().Get("Location"))
			}
		}
		if got := b.post(turn.path+"/messages", url.Values{"message": {turn.message}}); got.Code != 303 {
			t.Fatalf("question admission: %d", got.Code)
		}
		page := waitBuilder(t, b, turn.path, "succeeded")
		if !strings.Contains(page, turn.message) || !strings.Contains(page, "منظورتان آشنایی") || !strings.Contains(page, `data-total-tokens="7"`) || strings.Contains(page, `data-draft-revision=`) {
			t.Fatal("general history/accounting missing or synthetic Draft exposed")
		}
		request := <-requests
		if !strings.Contains(request, "Booking request") || !strings.Contains(request, "single_choice") || !strings.Contains(request, "clarifying question") {
			t.Fatal("supported product/uncertain intent context missing")
		}
		if strings.Contains(request, "حافظه نخست") && strings.Contains(request, "حافظه دوم") {
			t.Fatal("general chat memory leaked")
		}
	}
	if got := b.send("GET", "/bots/1", nil); got.Code != 404 {
		t.Fatal("question created a Bot")
	}
	page := b.send("GET", "/builder", nil)
	if !strings.Contains(page.Body.String(), `href="/chats/1"`) || !strings.Contains(page.Body.String(), `href="/chats/2"`) {
		t.Fatal("saved chats inaccessible")
	}
}

func TestPikoChatRejectsMutationToolsAndRequiresOwnerPOSTCSRF(t *testing.T) {
	var calls atomic.Int64
	_, b := generalBuilderFixture(t, builder.Config{MaxCalls: 1}, "error", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
	})
	path := startPikoChat(t, b)
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("other-piko@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, suffix := range []string{"", "/status", "/stream"} {
		if got := other.send("GET", path+suffix, nil); got.Code != 404 {
			t.Fatalf("owner isolation at %s: %d", suffix, got.Code)
		}
	}
	for _, suffix := range []string{"/messages", "/delete", "/runs/1/stop", "/runs/1/retry"} {
		if got := other.post(path+suffix, url.Values{"message": {"private"}}); got.Code != 404 {
			t.Fatalf("cross-owner action %s: %d", suffix, got.Code)
		}
		if got := b.send("GET", path+suffix, nil); got.Code != 405 {
			t.Fatalf("non-POST action %s: %d", suffix, got.Code)
		}
		if got := b.send("POST", path+suffix, url.Values{"csrf_token": {"wrong"}}); got.Code != 403 {
			t.Fatalf("CSRF action %s: %d", suffix, got.Code)
		}
	}
	for _, messages := range [][]string{nil, {" "}, {"one", "two"}, {strings.Repeat("س", 32769)}} {
		if got := b.post(path+"/messages", url.Values{"message": messages}); got.Code != 422 {
			t.Fatal("invalid question admitted", got.Code)
		}
	}
	if got := b.send("POST", "/chats", url.Values{}); got.Code != 403 {
		t.Fatal("chat creation lacks CSRF")
	}
	if calls.Load() != 0 {
		t.Fatal("rejections invoked provider")
	}
	if got := b.post(path+"/messages", url.Values{"message": {"آیا فرم دارید؟"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, path, "failed")
	if got := b.send("GET", "/bots/1", nil); got.Code != 404 || calls.Load() != 1 {
		t.Fatal("unsolicited mutation escaped general run boundary")
	}
}

func TestPikoChatSerializesOnlyItsOwnWorkAndStopDeletionRetainAllowance(t *testing.T) {
	started := make(chan struct{}, 4)
	cancelled := make(chan struct{}, 4)
	var calls atomic.Int64
	a, b := generalBuilderFixture(t, builder.Config{DailyRequests: 3}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	path, second := startPikoChat(t, b), startPikoChat(t, b)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- b.post(path+"/messages", url.Values{"message": {"پرسش همزمان"}}).Code
		}()
	}
	wg.Wait()
	counts := map[int]int{}
	counts[<-results]++
	counts[<-results]++
	if counts[303] != 1 || counts[409] != 1 {
		t.Fatal("concurrent requests bypassed chat fence", counts)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	if got := b.post(second+"/messages", url.Values{"message": {"پرسش مستقل"}}); got.Code != 303 {
		t.Fatal("unrelated chat blocked", got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("independent run did not start")
	}
	for range 2 {
		if got := b.send("GET", path+"/status", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `"running"`) {
			t.Fatal("status reconnect failed")
		}
	}
	if got := b.post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal("Stop failed", got.Code)
	}
	waitBuilder(t, b, path, "stopped")
	if got := b.post(path+"/runs/1/retry", url.Values{}); got.Code != 409 {
		t.Fatal("stopped work became retryable")
	}
	if got := b.send("GET", second+"/status", nil); !strings.Contains(got.Body.String(), `"running"`) {
		t.Fatal("Stop cancelled unrelated chat")
	}
	if got := b.post(second+"/delete", url.Values{}); got.Code != 303 {
		t.Fatal("running chat deletion failed")
	}
	for range 2 {
		select {
		case <-cancelled:
		case <-time.After(3 * time.Second):
			t.Fatal("Stop/delete left provider running")
		}
	}
	if got := b.send("GET", second, nil); got.Code != 404 {
		t.Fatal("deleted chat still accessible")
	}
	page := b.send("GET", path, nil).Body.String()
	if !strings.Contains(page, `data-admitted="2"`) || calls.Load() != 2 {
		t.Fatal("Stop/delete/reconnect lost or repeated accounting")
	}
	if got := b.post(path+"/messages", url.Values{"message": {"درخواست آخر"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("final run did not start")
	}
	if got := b.post(path+"/runs/3/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post(path+"/messages", url.Values{"message": {"بیش از سهمیه"}}); got.Code != 429 {
		t.Fatal("allowance bypassed", got.Code)
	}
	a.builder.Wait()
}

func TestPikoChatRestartRequiresExplicitRetryAndReloginKeepsHistory(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	a, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ پس از بازیابی"},"finish_reason":"stop"}]}`))
	})
	path := startPikoChat(t, b)
	if got := b.post(path+"/messages", url.Values{"message": {"پرسش ماندگار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	b.post("/logout", url.Values{})
	b.send("GET", "/login", nil)
	if got := b.post("/login", url.Values{"email": {"builder-owner@example.test"}, "password": {"OwnerPassword123"}}); got.Code != 303 {
		t.Fatal("relogin failed", got.Code)
	}
	if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), `data-run-status="running"`) {
		t.Fatal("logout stopped admitted work")
	}
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	page := waitBuilder(t, b, path, "interrupted")
	if !strings.Contains(page, "پرسش ماندگار") || !strings.Contains(page, `action="`+path+`/runs/1/retry"`) {
		t.Fatal("restart lost history or recovery")
	}
	server := httptest.NewServer(restarted.server.Handler)
	defer server.Close()
	for range 2 {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path+"/stream", nil)
		for _, cookie := range b.jar.Cookies(b.base) {
			req.AddCookie(cookie)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), `"interrupted"`) {
			t.Fatal("stream reconnect failed", response.StatusCode, string(body))
		}
	}
	if calls.Load() != 1 {
		t.Fatal("restart/reconnect replayed generation")
	}
	if got := b.post(path+"/runs/1/retry", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, path, "succeeded")
	if got := b.post(path+"/runs/1/retry", url.Values{}); got.Code != 409 || calls.Load() != 2 {
		t.Fatal("stale retry replayed work")
	}
}
