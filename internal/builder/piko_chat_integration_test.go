package builder_test

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
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestPikoChatUnavailableAndTimeoutKeepHonestSavedState(t *testing.T) {
	_, disabled := fixture.UnconnectedFixture(t)
	path := fixture.StartPikoChat(t, disabled)
	page := disabled.Send("GET", path, nil)
	if page.Code != 200 || strings.Contains(page.Body.String(), `action="`+path+`/messages"`) || !strings.Contains(page.Body.String(), "disabled") {
		t.Fatal("unavailable generation offers enabled composer")
	}
	if got := disabled.Post(path+"/messages", url.Values{"message": {"پرسش"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-admitted="0"`) {
		t.Fatal("unavailable generation admitted work")
	}
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{RunTimeout: 100 * time.Millisecond}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	path = fixture.StartPikoChat(t, b)
	for range 2 {
		if got := b.Post(path+"/messages", url.Values{"message": {"پرسش کند"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := fixture.WaitBuilder(t, b, path, "timeout")
		if !strings.Contains(page, "پرسش کند") || strings.Contains(page, `data-after-revision=`) {
			t.Fatal("timeout lost question or applied a Draft")
		}
	}
}

func TestPikoChatPrivateSummariesAndExistingBuilderDataSurviveForwardMigration(t *testing.T) {
	requests := make(chan string, 32)
	var summaries atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			fixture.MemoryReply(w, "یادآوری خصوصی اول")
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	// Exercise populated pre-upgrade chats, including a durable summary and paid runs.
	for turn := range 8 {
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {fixture.MemoryText(fmt.Sprintf("پیام پیشین %d", turn), 13000)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
	before := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	draft := b.LoadDraft(t, 1).Encode()
	fixture.RollbackToMigration(t, a.DB, "000016_builder_memory")
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if after := b.Send("GET", "/bots/1/chats/1", nil); after.Code != 200 || after.Body.String() != before {
		t.Fatal("migration changed existing history, runs, session or accounting")
	}
	if after := b.LoadDraft(t, 1).Encode(); after != draft {
		t.Fatal("migration changed existing Draft")
	}
	for len(requests) > 0 {
		<-requests
	}
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه پس از مهاجرت"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if request := <-requests; !strings.Contains(request, "یادآوری خصوصی اول") {
		t.Fatal("migration lost existing summary")
	}
	path := fixture.StartPikoChat(t, b)
	for turn := range 8 {
		if got := b.Post(path+"/messages", url.Values{"message": {fixture.MemoryText(fmt.Sprintf("پرسش خصوصی %d", turn), 13000)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := fixture.WaitBuilder(t, b, path, "succeeded")
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
	if summaries.Load() < 2 {
		t.Fatal("general and Bot chats did not each summarize")
	}
	if got := b.Post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Send("GET", path, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "پرسش خصوصی 0") {
		t.Fatal("Bot deletion removed unrelated general chat")
	}
	if got := b.Send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("Bot deletion retained associated history")
	}
}

func TestPikoQuestionsSaveIndependentConversationWithoutBot(t *testing.T) {
	requests := make(chan string, 3)
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
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
	if got := b.Send("GET", "/builder", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `action="/chats"`) {
		t.Fatal("Piko has no independent conversation entry")
	}
	for _, turn := range []struct{ path, message string }{
		{"/chats/1", "حافظه نخست: چه قالب\u200cهایی دارید؟"},
		{"/chats/2", "حافظه دوم: رزرو شاید"},
		{"/chats/1", "ادامه نخست"},
	} {
		if turn.message != "ادامه نخست" {
			got := b.Post("/chats", url.Values{})
			if got.Code != 303 || got.Header().Get("Location") != turn.path {
				t.Fatalf("start: %d %s", got.Code, got.Header().Get("Location"))
			}
		}
		if got := b.Post(turn.path+"/messages", url.Values{"message": {turn.message}}); got.Code != 303 {
			t.Fatalf("question admission: %d", got.Code)
		}
		page := fixture.WaitBuilder(t, b, turn.path, "succeeded")
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
	if got := b.Send("GET", "/bots/1", nil); got.Code != 404 {
		t.Fatal("question created a Bot")
	}
	page := b.Send("GET", "/builder", nil)
	if !strings.Contains(page.Body.String(), `href="/chats/1"`) || !strings.Contains(page.Body.String(), `href="/chats/2"`) {
		t.Fatal("saved chats inaccessible")
	}
}

func TestPikoChatRejectsMutationToolsAndRequiresOwnerPOSTCSRF(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{MaxCalls: 1}, "error", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
	})
	path := fixture.StartPikoChat(t, b)
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("other-piko@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, suffix := range []string{"", "/status", "/stream"} {
		if got := other.Send("GET", path+suffix, nil); got.Code != 404 {
			t.Fatalf("owner isolation at %s: %d", suffix, got.Code)
		}
	}
	for _, suffix := range []string{"/messages", "/runs/1/stop", "/runs/1/retry"} {
		if got := other.Post(path+suffix, url.Values{"message": {"private"}}); got.Code != 404 {
			t.Fatalf("cross-owner action %s: %d", suffix, got.Code)
		}
		if got := b.Send("GET", path+suffix, nil); got.Code != 405 {
			t.Fatalf("non-POST action %s: %d", suffix, got.Code)
		}
		if got := b.Send("POST", path+suffix, url.Values{"csrf_token": {"wrong"}}); got.Code != 403 {
			t.Fatalf("CSRF action %s: %d", suffix, got.Code)
		}
	}
	for _, messages := range [][]string{nil, {" "}, {"one", "two"}, {strings.Repeat("س", 32769)}} {
		if got := b.Post(path+"/messages", url.Values{"message": messages}); got.Code != 422 {
			t.Fatal("invalid question admitted", got.Code)
		}
	}
	if got := b.Send("POST", "/chats", url.Values{}); got.Code != 403 {
		t.Fatal("chat creation lacks CSRF")
	}
	if calls.Load() != 0 {
		t.Fatal("rejections invoked provider")
	}
	if got := b.Post(path+"/messages", url.Values{"message": {"آیا فرم دارید؟"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, path, "failed")
	if got := b.Send("GET", "/bots/1", nil); got.Code != 404 || calls.Load() != 1 {
		t.Fatal("unsolicited mutation escaped general run boundary")
	}
}

func TestPikoChatSerializesOnlyItsOwnWorkAndStopDeletionRetainAllowance(t *testing.T) {
	started := make(chan struct{}, 4)
	cancelled := make(chan struct{}, 4)
	var calls atomic.Int64
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{DailyRequests: 3}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	})
	path, second := fixture.StartPikoChat(t, b), fixture.StartPikoChat(t, b)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- b.Post(path+"/messages", url.Values{"message": {"پرسش همزمان"}}).Code
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
	if got := b.Post(second+"/messages", url.Values{"message": {"پرسش مستقل"}}); got.Code != 303 {
		t.Fatal("unrelated chat blocked", got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("independent run did not start")
	}
	for range 2 {
		if got := b.Send("GET", path+"/status", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `"running"`) {
			t.Fatal("status reconnect failed")
		}
	}
	if got := b.Post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal("Stop failed", got.Code)
	}
	fixture.WaitBuilder(t, b, path, "stopped")
	if got := b.Post(path+"/runs/1/retry", url.Values{}); got.Code != 409 {
		t.Fatal("stopped work became retryable")
	}
	if got := b.Send("GET", second+"/status", nil); !strings.Contains(got.Body.String(), `"running"`) {
		t.Fatal("Stop cancelled unrelated chat")
	}
	if got := b.Post(second+"/runs/2/stop", url.Values{}); got.Code != 303 {
		t.Fatal("independent chat Stop failed")
	}
	for range 2 {
		select {
		case <-cancelled:
		case <-time.After(3 * time.Second):
			t.Fatal("Stop left provider running")
		}
	}
	if got := b.Send("GET", second, nil); got.Code != 200 {
		t.Fatal("stopped chat lost history")
	}
	page := b.Send("GET", path, nil).Body.String()
	if !strings.Contains(page, `data-admitted="2"`) || calls.Load() != 2 {
		t.Fatal("Stop/reconnect lost or repeated accounting")
	}
	if got := b.Post(path+"/messages", url.Values{"message": {"درخواست آخر"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("final run did not start")
	}
	if got := b.Post(path+"/runs/3/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post(path+"/messages", url.Values{"message": {"بیش از سهمیه"}}); got.Code != 429 {
		t.Fatal("allowance bypassed", got.Code)
	}
	a.Builder.Wait()
}

func TestPikoChatRestartRequiresExplicitRetryAndReloginKeepsHistory(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ پس از بازیابی"},"finish_reason":"stop"}]}`))
	})
	path := fixture.StartPikoChat(t, b)
	if got := b.Post(path+"/messages", url.Values{"message": {"پرسش ماندگار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	b.Post("/logout", url.Values{})
	b.Send("GET", "/login", nil)
	if got := b.Post("/login", url.Values{"email": {"builder-owner@example.test"}, "password": {"OwnerPassword123"}}); got.Code != 303 {
		t.Fatal("relogin failed", got.Code)
	}
	if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), `data-run-status="running"`) {
		t.Fatal("logout stopped admitted work")
	}
	a.StopWork()
	a.Builder.Wait()
	_ = a.DB.Close()
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	page := fixture.WaitBuilder(t, b, path, "interrupted")
	if !strings.Contains(page, "پرسش ماندگار") || !strings.Contains(page, `action="`+path+`/runs/1/retry"`) {
		t.Fatal("restart lost history or recovery")
	}
	server := httptest.NewServer(restarted.Handler)
	defer server.Close()
	for range 2 {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path+"/stream", nil)
		for _, cookie := range b.Jar.Cookies(b.Base) {
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
	if got := b.Post(path+"/runs/1/retry", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, path, "succeeded")
	if got := b.Post(path+"/runs/1/retry", url.Values{}); got.Code != 409 || calls.Load() != 2 {
		t.Fatal("stale retry replayed work")
	}
}
