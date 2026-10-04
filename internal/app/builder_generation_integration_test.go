package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
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
	"github.com/pooya79/Piko/internal/testsupport"
)

func builderFixture(t *testing.T, config builder.Config, provider http.HandlerFunc) (*App, *accountBrowser) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := httptest.NewServer(streamingBuilderProvider(provider))
	t.Cleanup(fake.Close)
	config.APIKey, config.BaseURL = "test-server-key", fake.URL+"/v1"
	cfg := Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "builder-test-secret-at-least-32-characters", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", LogLevel: "error", ShutdownPeriod: time.Second, Builder: config}
	a, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.stopRequests(); a.builder.Wait(); _ = a.db.Close() })
	b := newAccountBrowser(t, a.server.Handler)
	b.send("GET", "/register", nil)
	if got := b.post("/register", registerValues("builder-owner@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/new", url.Values{"name": {"ربات گفتگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"گفتگوی اول", "گفتگوی دوم"} {
		if got := b.post("/bots/1/chats", url.Values{"title": {title}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	return a, b
}

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
			_, b := builderFixture(t, builder.Config{DailyRequests: 1, MaxCalls: 1}, func(w http.ResponseWriter, r *http.Request) {
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
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			status := "failed"
			if tc.name == "missing usage" || tc.name == "explicit zero" {
				status = "succeeded"
			}
			page := waitBuilder(t, b, "/bots/1/chats/1", status)
			if strings.Contains(page, "test-server-key") || strings.Contains(page, "private upstream") || !strings.Contains(page, `data-total-tokens="`+tc.total+`"`) || !strings.Contains(page, `data-cost="`+tc.cost+`"`) {
				t.Fatal("feedback or reported accounting wrong")
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); before.Encode() != after.Encode() {
				t.Fatal("conversational request changed Draft")
			}
			if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"دوباره"}}); got.Code != 429 || calls.Load() != 1 {
				t.Fatal("failed admission/retries escaped budget")
			}
		})
	}
}

func TestBuilderGenerationRequiresOwnerPOSTCSRFAndValidInput(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	path := "/bots/1/chats/1/messages"
	for _, input := range [][]string{nil, {" "}, {"one", "two"}, {strings.Repeat("س", 32769)}} {
		if got := b.post(path, url.Values{"message": input}); got.Code != 422 {
			t.Fatalf("invalid input: %d", got.Code)
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		if got := b.send("POST", path, url.Values{"message": {"rejected"}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal(got.Code)
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if got := b.send(method, path+"?csrf_token="+url.QueryEscape(b.cookie("piko_csrf")), nil); got.Code != 405 {
			t.Fatal(got.Code)
		}
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("other-builder@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := other.post(path, url.Values{"message": {"unauthorized"}}); got.Code != 404 {
		t.Fatal("other owner admitted")
	}
	if got := b.post("/bots/1/chats/999/messages", url.Values{"message": {"unknown"}}); got.Code != 404 {
		t.Fatal("missing chat admitted")
	}
	guest := newAccountBrowser(t, b.router)
	guest.send("GET", "/login", nil)
	if got := guest.post(path, url.Values{"message": {"guest"}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("guest admitted")
	}
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-admitted="0"`) || calls.Load() != 0 {
		t.Fatal("rejections consumed allowance")
	}
}

func TestBuilderMalformedCostCannotExpandUnboundedDecimal(t *testing.T) {
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ"},"finish_reason":"stop"}],"usage":{"cost":1e1000000}}`))
	})
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"سلام"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-cost="unknown"`) {
		t.Fatal("unbounded cost was retained")
	}
}

func TestBuilderWithoutCredentialsKeepsHistoryAndManualFeaturesAvailable(t *testing.T) {
	_, b := unconnectedFixture(t)
	b.post("/bots/new", url.Values{"name": {"بدون کلید"}})
	b.post("/bots/1/chats", url.Values{"title": {"گفتگو"}})
	page := b.send("GET", "/bots/1/chats/1", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "disabled") || strings.Contains(page.Body.String(), `action="/bots/1/chats/1/messages"`) {
		t.Fatal("missing key not handled before initialization")
	}
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-admitted="0"`) {
		t.Fatal("disabled request admitted")
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal("manual Draft disabled")
	}
	if got := b.post("/bots/1/preview", url.Values{}); got.Code != 303 {
		t.Fatal("Preview disabled")
	}
}

func TestBuilderRunTimeoutSavesOutcomeAndReleasesBusyState(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{RunTimeout: 100 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		<-r.Context().Done()
	})
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/2"} {
		if got := b.post(path+"/messages", url.Values{"message": {"کند"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := waitBuilder(t, b, path, "timeout")
		if !strings.Contains(page, `data-total-tokens="unknown"`) {
			t.Fatal("timeout invented usage")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("timeout did not release Bot")
	}
}

func TestBuilderShutdownCancelsAndJoinsProviderBeforeDatabaseClose(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	stop := startBuilderApp(t, a)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"باقی می ماند"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	stop()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider not cancelled")
	}
	restarted, err := New(context.Background(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	page := waitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	if !strings.Contains(page, "باقی می ماند") || !strings.Contains(page, `data-admitted="1"`) {
		t.Fatal("shutdown lost admitted request")
	}
}

func startBuilderApp(t *testing.T, a *App) func() {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.server.Addr = listener.Addr().String()
	_ = listener.Close()
	stop := runDeliveryApp(t, a)
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := http.Get("http://" + a.server.Addr + "/health/live")
		if err == nil {
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(time.Millisecond)
	}
	return stop
}

func TestBuilderStartingAnotherAppPreservesLiveRunLease(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"نتیجه محفوظ"},"finish_reason":"stop"}]}`))
	})
	defer close(release)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	second, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	startBuilderApp(t, second)
	other := newAccountBrowser(t, second.server.Handler)
	other.jar.SetCookies(other.base, b.jar.Cookies(b.base))
	if page := other.send("GET", "/bots/1/chats/1", nil); !strings.Contains(page.Body.String(), `data-run-status="running"`) {
		t.Fatal("startup interrupted live run")
	}
	if got := other.post("/bots/1/chats/2/messages", url.Values{"message": {"رقابت"}}); got.Code != 409 {
		t.Fatal("startup released live Bot")
	}
	release <- struct{}{}
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
}

func TestBuilderRecoversAbandonedRunWithoutRepeatingProviderCall(t *testing.T) {
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	// A crashed process left committed admission and owner history, with no live lease.
	if _, err := a.db.Exec(`INSERT INTO builder_runs(owner_id,bot_id,chat_id,day,model,draft_revision,status,created_at,lease_until) VALUES(1,1,1,?,'openai/gpt-6-luna',1,'running',1,0)`, time.Now().UTC().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'owner','درخواست پیش از توقف',1)`); err != nil {
		t.Fatal(err)
	}
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	startBuilderApp(t, restarted)
	b.router = restarted.server.Handler
	page := waitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	if !strings.Contains(page, "درخواست پیش از توقف") || !strings.Contains(page, `data-admitted="1"`) || calls.Load() != 0 {
		t.Fatal("abandoned request lost or replayed")
	}
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"تلاش صریح"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if calls.Load() != 1 {
		t.Fatal("explicit retry did not release Bot")
	}
}

func TestBuilderAdmissionStorageFailureDoesNotConsumeAllowance(t *testing.T) {
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	if _, err := a.db.Exec(`CREATE TRIGGER fail_builder_request BEFORE INSERT ON builder_messages WHEN NEW.role='owner' BEGIN SELECT RAISE(ABORT,'private admission failure'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"rollback"}}); got.Code != 500 || strings.Contains(got.Body.String(), "private admission failure") {
		t.Fatal("unsafe admission failure")
	}
	page := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(page, `data-admitted="0"`) || strings.Contains(page, `data-run-status=`) || calls.Load() != 0 {
		t.Fatal("partial admission persisted")
	}
}

func TestBuilderAdmissionIsBusyWithoutLockingDraftAndRetainsAllowanceAfterDeletion(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{DailyRequests: 1, MaxCalls: 1}, func(w http.ResponseWriter, r *http.Request) {
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
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست اول"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not called")
	}
	if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"درخواست ردشده"}}); got.Code != 409 || !strings.Contains(got.Body.String(), `data-admitted="1"`) {
		t.Fatal("busy request was admitted or unclear")
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal("provider I/O held a write transaction")
	}
	// Releasing through a separate channel avoids closing twice in deferred cleanup.
	release <- struct{}{}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-cost="0"`) {
		t.Fatal("reported zero became unknown")
	}
	if got := b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page = b.send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(page, `data-admitted="1"`) || !strings.Contains(page, `data-total-tokens="6"`) || !strings.Contains(page, `data-cost="0"`) {
		t.Fatal("deletion erased allowance/accounting")
	}
	if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"تلاش دوباره"}}); got.Code != 429 || calls.Load() != 1 {
		t.Fatal("daily allowance bypassed")
	}
}

func TestBuilderFailedReplyPersistenceSavesFailedOutcomeAndRetainsUsage(t *testing.T) {
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ ذخیره نشده"},"finish_reason":"stop"}],"usage":{"total_tokens":8,"cost":0.002}}`))
	})
	if _, err := a.db.Exec(`CREATE TRIGGER fail_builder_reply BEFORE INSERT ON builder_messages WHEN NEW.role='model' BEGIN SELECT RAISE(ABORT,'secret storage error'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if strings.Contains(page, "پاسخ ذخیره نشده") || strings.Contains(page, "secret storage error") || !strings.Contains(page, "درخواست محفوظ") || !strings.Contains(page, `data-total-tokens="8"`) {
		t.Fatal("unsafe or partial persistence")
	}
}

func TestBuilderConcurrentBotAdmissionAcceptsExactlyOneRequest(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
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
	second, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.stopRequests(); second.builder.Wait(); _ = second.db.Close() })
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for i, path := range []string{"/bots/1/chats/1/messages", "/bots/1/chats/2/messages"} {
		router := b.router
		if i == 1 {
			router = second.server.Handler
		}
		browser := newAccountBrowser(t, router)
		browser.jar.SetCookies(browser.base, b.jar.Cookies(b.base))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- browser.post(path, url.Values{"message": {"رقابت"}}).Code
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
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-admitted="1"`) {
		t.Fatal("busy rejection counted")
	}
}

func TestBuilderDailyAccountingSumsReportedMetricsDespiteMissingUsage(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		usage := ""
		if calls.Add(1) == 1 {
			usage = `,"usage":{"total_tokens":17,"cost":0.0012}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"پاسخ"},"finish_reason":"stop"}]` + usage + `}`))
	})
	for range 2 {
		if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"گفتگو"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
	page := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	last := strings.LastIndex(page, `data-total-tokens=`)
	if last < 0 || !strings.HasPrefix(page[last:], `data-total-tokens="17"`) || !strings.Contains(page[last:], `data-cost="0.0012"`) || !strings.Contains(page[last:], `data-usage-complete="false"`) {
		t.Fatal("reported sum lost or missing usage hidden")
	}
}

func waitBuilder(t *testing.T, b *accountBrowser, path, status string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page := b.send("GET", path, nil)
		if page.Code != 200 {
			t.Fatal(page.Code)
		}
		body := page.Body.String()
		last := strings.LastIndex(body, `data-run-status=`)
		if last >= 0 && strings.HasPrefix(body[last:], `data-run-status="`+status+`"`) {
			return page.Body.String()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Builder outcome did not become " + status)
	return ""
}

func TestBuilderRepliesUseOwnHistoryAndCurrentDraftAndSurviveRestart(t *testing.T) {
	requests := make(chan string, 4)
	a, b := builderFixture(t, builder.Config{Model: "vendor/configured-model"}, func(w http.ResponseWriter, r *http.Request) {
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
			v := inquiryDraft()
			v.Set("welcome", "پیش نویس تازه")
			if got := b.post("/bots/1/draft", draftAtRevision(v, "1")); got.Code != 303 {
				t.Fatal(got.Code)
			}
		}
		if got := b.post(turn.path+"/messages", url.Values{"message": {turn.message}}); got.Code != 303 {
			t.Fatalf("admit: %d", got.Code)
		}
		body := waitBuilder(t, b, turn.path, "succeeded")
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
	before := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	a.stopRequests()
	a.builder.Wait()
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if after := b.send("GET", "/bots/1/chats/1", nil).Body.String(); before != after {
		t.Fatal("restart lost saved outcome/history")
	}
	if err := database.Migrate(t.Context(), restarted.db, false); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderShutdownBoundsAccountingUnderSQLiteContention(t *testing.T) {
	started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply"},"finish_reason":"stop"}],"usage":{"total_tokens":8}}`))
		close(returned)
	})
	stop := startBuilderApp(t, a)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"request"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	<-started
	blocker, err := database.Open(t.Context(), a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	tx, err := blocker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	<-returned
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
		_ = tx.Rollback()
	case <-time.After(a.cfg.ShutdownPeriod + 500*time.Millisecond):
		_ = tx.Rollback()
		<-done
		t.Error("Builder shutdown exceeded configured one-second ShutdownPeriod while accounting waited on SQLite")
	}
}

func TestBuilderShutdownBoundsAdmissionUnderSQLiteContention(t *testing.T) {
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	stop := startBuilderApp(t, a)
	blocker, err := database.Open(t.Context(), a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	tx, err := blocker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan int, 1)
	go func() { requestDone <- b.post("/bots/1/chats/1/messages", url.Values{"message": {"request"}}).Code }()
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
		_ = tx.Rollback()
	case <-time.After(a.cfg.ShutdownPeriod + 500*time.Millisecond):
		_ = tx.Rollback()
		<-done
		t.Error("Builder shutdown exceeded configured one-second ShutdownPeriod while admission waited on SQLite")
	}
	<-requestDone
}
