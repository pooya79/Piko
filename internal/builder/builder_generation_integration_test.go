package builder_test

import (
	"encoding/json"
	"io"
	"net/http"
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

func TestBuilderProviderFailuresKeepSafeDurableOutcomesAndReportedUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		code        int
		total, cost string
	}{
		{"missing usage", `{"choices":[{"message":{"role":"assistant","content":"پاسخ بدون حساب"},"finish_reason":"stop"}]}`, 200, "unknown", "unknown"},
		{"explicit zero", `{"choices":[{"message":{"role":"assistant","content":"پاسخ رایگان"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"cost":0}}`, 200, "0", "0"},
		{"provider error", `{"error":{"message":"test-server-key private upstream description"}}`, 500, "unknown", "unknown"},
		{"malformed", `{"choices":`, 200, "unknown", "unknown"},
		{"empty reply", `{"choices":[{"message":{"role":"assistant","content":" "},"finish_reason":"stop"}],"usage":{"total_tokens":3,"cost":0.0003}}`, 200, "3", "0.0003"},
		{"truncated reply", `{"choices":[{"message":{"role":"assistant","content":"incomplete response"},"finish_reason":"length"}],"usage":{"total_tokens":9}}`, 200, "9", "unknown"},
		{"no choices", `{"choices":[],"usage":{"total_tokens":12,"cost":0.01}}`, 200, "12", "0.01"},
		{"unsolicited tool", `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"edit_draft","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":4}}`, 200, "4", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			_, b := fixture.BuilderFixture(t, builder.Config{DailyRequests: 1, MaxCalls: 1}, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var req map[string]any
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req["model"] != "openai/gpt-6-luna" {
					t.Error("default model changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			})
			before := b.LoadDraft(t, 1)
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			status := "failed"
			if tc.name == "missing usage" || tc.name == "explicit zero" {
				status = "succeeded"
			}
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", status)
			if strings.Contains(page, "test-server-key") || strings.Contains(page, "private upstream") || !strings.Contains(page, `data-total-tokens="`+tc.total+`"`) || !strings.Contains(page, `data-cost="`+tc.cost+`"`) {
				t.Fatal("feedback or reported accounting wrong")
			}
			if after := b.LoadDraft(t, 1); before.Encode() != after.Encode() {
				t.Fatal("conversational request changed Draft")
			}
			if got := b.Post("/bots/1/chats/2/messages", url.Values{"message": {"دوباره"}}); got.Code != 429 || calls.Load() != 1 {
				t.Fatal("failed admission/retries escaped budget")
			}
		})
	}
}

func TestBuilderGenerationRequiresOwnerPOSTCSRFAndValidInput(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	path := "/bots/1/chats/1/messages"
	for _, input := range [][]string{nil, {" "}, {"one", "two"}, {strings.Repeat("س", 32769)}} {
		if got := b.Post(path, url.Values{"message": input}); got.Code != 422 {
			t.Fatalf("invalid input: %d", got.Code)
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		if got := b.Send("POST", path, url.Values{"message": {"rejected"}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal(got.Code)
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if got := b.Send(method, path+"?csrf_token="+url.QueryEscape(b.Cookie("piko_csrf")), nil); got.Code != 405 {
			t.Fatal(got.Code)
		}
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("other-builder@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := other.Post(path, url.Values{"message": {"unauthorized"}}); got.Code != 404 {
		t.Fatal("other owner admitted")
	}
	if got := b.Post("/bots/1/chats/999/messages", url.Values{"message": {"unknown"}}); got.Code != 404 {
		t.Fatal("missing chat admitted")
	}
	guest := fixture.NewAccountBrowser(t, b.Router)
	guest.Send("GET", "/login", nil)
	if got := guest.Post(path, url.Values{"message": {"guest"}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("guest admitted")
	}
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-admitted="0"`) || calls.Load() != 0 {
		t.Fatal("rejections consumed allowance")
	}
}

func TestBuilderMalformedCostCannotExpandUnboundedDecimal(t *testing.T) {
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ"},"finish_reason":"stop"}],"usage":{"cost":1e1000000}}`))
	})
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"سلام"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-cost="unknown"`) {
		t.Fatal("unbounded cost was retained")
	}
}

func TestBuilderWithoutCredentialsKeepsHistoryAndManualFeaturesAvailable(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	b.Post("/bots/new", url.Values{"name": {"بدون کلید"}})
	fixture.SeedBotChat(t, a.DB, 1, "گفتگو")
	page := b.Send("GET", "/bots/1/chats/1", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "disabled") || strings.Contains(page.Body.String(), `action="/bots/1/chats/1/messages"`) {
		t.Fatal("missing key not handled before initialization")
	}
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-admitted="0"`) {
		t.Fatal("disabled request admitted")
	}
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.InquiryDraft(), "1")); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/preview", url.Values{}); got.Code != 303 {
		t.Fatal("Preview disabled")
	}
}

func TestBuilderRunTimeoutSavesOutcomeAndReleasesBusyState(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{RunTimeout: 100 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		<-r.Context().Done()
	})
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/2"} {
		if got := b.Post(path+"/messages", url.Values{"message": {"کند"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := fixture.WaitBuilder(t, b, path, "timeout")
		if !strings.Contains(page, `data-total-tokens="unknown"`) {
			t.Fatal("timeout invented usage")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("timeout did not release Bot")
	}
}

func TestBuilderAdmissionStorageFailureDoesNotConsumeAllowance(t *testing.T) {
	var calls atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_builder_request BEFORE INSERT ON builder_messages WHEN NEW.role='owner' BEGIN SELECT RAISE(ABORT,'private admission failure'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"rollback"}}); got.Code != 500 || strings.Contains(got.Body.String(), "private admission failure") {
		t.Fatal("unsafe admission failure")
	}
	page := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(page, `data-admitted="0"`) || strings.Contains(page, `data-run-status=`) || calls.Load() != 0 {
		t.Fatal("partial admission persisted")
	}
}

func TestBuilderAdmissionIsBusyWithoutLockingDraftAndRetainsAllowanceAfterRejectedDeletion(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{DailyRequests: 1, MaxCalls: 1}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ محفوظ"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6,"cost":0}}`))
	})
	defer close(release)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست اول"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not called")
	}
	if got := b.Post("/bots/1/chats/2/messages", url.Values{"message": {"درخواست ردشده"}}); got.Code != 409 || !strings.Contains(got.Body.String(), `data-admitted="1"`) {
		t.Fatal("busy request was admitted or unclear")
	}
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.InquiryDraft(), "1")); err != nil {
		t.Fatal(err)
	}
	// Releasing through a separate channel avoids closing twice in deferred cleanup.
	release <- struct{}{}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-cost="0"`) {
		t.Fatal("reported zero became unknown")
	}
	if got := b.Post("/bots/1/chats/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal(got.Code)
	}
	page = b.Send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(page, `data-admitted="1"`) || !strings.Contains(page, `data-total-tokens="6"`) || !strings.Contains(page, `data-cost="0"`) {
		t.Fatal("rejected deletion erased allowance/accounting")
	}
	if got := b.Post("/bots/1/chats/2/messages", url.Values{"message": {"تلاش دوباره"}}); got.Code != 429 || calls.Load() != 1 {
		t.Fatal("daily allowance bypassed")
	}
}

func TestBuilderFailedReplyPersistenceSavesFailedOutcomeAndRetainsUsage(t *testing.T) {
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ ذخیره نشده"},"finish_reason":"stop"}],"usage":{"total_tokens":8,"cost":0.002}}`))
	})
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_builder_reply BEFORE INSERT ON builder_messages WHEN NEW.role='model' BEGIN SELECT RAISE(ABORT,'secret storage error'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if strings.Contains(page, "پاسخ ذخیره نشده") || strings.Contains(page, "secret storage error") || !strings.Contains(page, "درخواست محفوظ") || !strings.Contains(page, `data-total-tokens="8"`) {
		t.Fatal("unsafe or partial persistence")
	}
}

func TestBuilderConcurrentBotAdmissionAcceptsExactlyOneRequest(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"تمام"},"finish_reason":"stop"}]}`))
	})
	defer close(release)
	second, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.StopWork(); second.Builder.Wait(); _ = second.DB.Close() })
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i, path := range []string{"/bots/1/chats/1/messages", "/bots/1/chats/2/messages"} {
		router := b.Router
		if i == 1 {
			router = second.Handler
		}
		browser := fixture.NewAccountBrowser(t, router)
		browser.Jar.SetCookies(browser.Base, b.Jar.Cookies(b.Base))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- browser.Post(path, url.Values{"message": {"رقابت"}}).Code
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[303] != 1 || counts[409] != 1 {
		t.Fatalf("admission results: %v", counts)
	}
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-admitted="1"`) {
		t.Fatal("busy rejection counted")
	}
}

func TestBuilderDailyAccountingSumsReportedMetricsDespiteMissingUsage(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		usage := ""
		if calls.Add(1) == 1 {
			usage = `,"usage":{"total_tokens":17,"cost":0.0012}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ"},"finish_reason":"stop"}]` + usage + `}`))
	})
	for range 2 {
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"گفتگو"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
	page := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	last := strings.LastIndex(page, `data-total-tokens=`)
	if last < 0 || !strings.HasPrefix(page[last:], `data-total-tokens="17"`) || !strings.Contains(page[last:], `data-cost="0.0012"`) || !strings.Contains(page[last:], `data-usage-complete="false"`) {
		t.Fatal("reported sum lost or missing usage hidden")
	}
}

func TestBuilderRepliesUseOwnHistoryAndCurrentDraftAndSurviveRestart(t *testing.T) {
	requests := make(chan string, 4)
	a, b := fixture.BuilderFixture(t, builder.Config{Model: "vendor/configured-model"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-server-key" {
			t.Error("incorrect provider configuration")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "vendor/configured-model" {
			t.Error("configured model not used")
		}
		encoded, _ := json.Marshal(body)
		requests <- string(encoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"پاسخ واقعی <script>"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":7,"total_tokens":17,"cost":0.0012}}`))
	})
	for _, turn := range []struct{ path, message string }{{"/bots/1/chats/1", "حافظه اول"}, {"/bots/1/chats/2", "حافظه دوم"}, {"/bots/1/chats/1", "ادامه اول"}} {
		if turn.message == "ادامه اول" {
			v := fixture.InquiryDraft()
			v.Set("welcome", "پیش نویس تازه")
			if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(v, "1")); err != nil {
				t.Fatal(err)
			}
		}
		if got := b.Post(turn.path+"/messages", url.Values{"message": {turn.message}}); got.Code != 303 {
			t.Fatalf("admit: %d", got.Code)
		}
		body := fixture.WaitBuilder(t, b, turn.path, "succeeded")
		if !strings.Contains(body, "پاسخ واقعی &lt;script&gt;") || !strings.Contains(body, `data-total-tokens="17"`) || !strings.Contains(body, `data-cost="0.0012"`) {
			t.Fatal("reply or reported accounting missing")
		}
		request := <-requests
		if strings.Contains(request, "حافظه دوم") && strings.Contains(request, "حافظه اول") {
			t.Fatal("another chat entered context")
		}
		if turn.message == "ادامه اول" && (!strings.Contains(request, "پیش نویس تازه") || !strings.Contains(request, "حافظه اول") || !strings.Contains(request, "پاسخ واقعی")) {
			t.Fatal("current Draft or own history missing")
		}
	}
	before := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	a.StopWork()
	a.Builder.Wait()
	if err := a.DB.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	if after := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); before != after {
		t.Fatal("restart lost saved outcome/history")
	}
	if err := database.Migrate(t.Context(), restarted.DB, false); err != nil {
		t.Fatal(err)
	}
}
