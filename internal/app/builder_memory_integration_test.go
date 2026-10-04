package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
)

func memoryReply(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5, "cost": 0.001},
	})
}

func fillMemoryChat(t *testing.T, b *accountBrowser) {
	t.Helper()
	for turn := range 6 {
		if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {fmt.Sprintf("تاریخچه اصلی %d", turn)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
}

func TestBuilderSummaryFailurePreservesHistoryDraftAndRetriesHonestly(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		code       int
	}{
		{"provider", "", 500}, {"empty", " ", 200}, {"oversized", strings.Repeat("س", 4001), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int64
			requests := make(chan string, 4)
			_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "Summarize older messages") {
					requests <- string(body)
					if attempts.Add(1) == 1 {
						if tc.code != 200 {
							w.WriteHeader(tc.code)
							_, _ = w.Write([]byte(`{"error":{"message":"test-server-key private failure"},"usage":{"total_tokens":5,"cost":0.001}}`))
							return
						}
						memoryReply(w, tc.text)
						return
					}
					memoryReply(w, "خلاصه بازیابی شده")
					return
				}
				memoryReply(w, "پاسخ محفوظ")
			})
			fillMemoryChat(t, b)
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
			b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست پس از تاریخچه"}})
			page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
			if !strings.Contains(page, `data-run-result="memory.failed"`) || !strings.Contains(page, "تاریخچهٔ کامل") || !strings.Contains(page, "تاریخچه اصلی 0") || !strings.Contains(page, `data-admitted="7"`) || !strings.Contains(page, `data-total-tokens="35"`) || strings.Contains(page, "test-server-key") || strings.Contains(page, tc.text) && tc.name == "oversized" {
				t.Fatal("summary failure lost history/accounting or exposed content")
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
				t.Fatal("summary failure changed Draft")
			}
			b.post("/bots/1/chats/1/messages", url.Values{"message": {"تلاش تازه"}})
			waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			for range 2 {
				if request := <-requests; !strings.Contains(request, "تاریخچه اصلی 0") {
					t.Fatal("failed memory advanced boundary")
				}
			}
		})
	}
}

func TestBuilderSummarySharesModelBudgetAndDeletionPreservesAccounting(t *testing.T) {
	var summaries, calls atomic.Int64
	requests := make(chan string, 32)
	_, b := builderFixture(t, builder.Config{MaxCalls: 1, DailyRequests: 8}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		calls.Add(1)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			memoryReply(w, "خلاصه درست")
			return
		}
		memoryReply(w, "پاسخ محفوظ")
	})
	fillMemoryChat(t, b)
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست بلند"}})
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if summaries.Load() != 1 || calls.Load() != 7 || !strings.Contains(page, `data-run-result="call.limit"`) || !strings.Contains(page, `data-total-tokens="35"`) || !strings.Contains(page, `data-cost="0.007"`) {
		t.Fatal("summary bypassed budget or local accounting")
	}
	// Valid memory survives a later reply failure, without repeating summary charges.
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه"}})
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	var last string
	for len(requests) > 0 {
		last = <-requests
	}
	if !strings.Contains(last, "خلاصه درست") || strings.Contains(last, "تاریخچه اصلی 0") || summaries.Load() != 1 {
		t.Fatal("successful summary was lost after reply budget failure")
	}
	if got := b.post("/bots/1/chats/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"بیش از سهمیه"}}); got.Code != 429 || calls.Load() != 8 {
		t.Fatal("deletion bypassed daily allowance")
	}
	page = b.send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(page, `data-admitted="8"`) || !strings.Contains(page, `data-total-tokens="40"`) || !strings.Contains(page, `data-cost="0.008"`) {
		t.Fatal("summary deletion lost usage")
	}
}

func TestBuilderSummaryUsesRunDeadlineAndStopsWithoutBackgroundRetry(t *testing.T) {
	var summaries atomic.Int64
	_, b := builderFixture(t, builder.Config{RunTimeout: 200 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			<-r.Context().Done()
			return
		}
		memoryReply(w, "پاسخ محفوظ")
	})
	fillMemoryChat(t, b)
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست محدود"}})
	page := waitBuilder(t, b, "/bots/1/chats/1", "timeout")
	if summaries.Load() != 1 || !strings.Contains(page, "تاریخچه اصلی 0") || !strings.Contains(page, `data-admitted="7"`) {
		t.Fatal("summary deadline lost history or retried")
	}
}

func TestBuilderMemoryBoundsLargeMessagesAndForwardMigrationPreservesData(t *testing.T) {
	requests := make(chan string, 16)
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			memoryReply(w, "خلاصه متن بلند")
			return
		}
		memoryReply(w, "پاسخ محفوظ")
	})
	fillMemoryChat(t, b)
	before := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	rollbackToMigration(t, a.db, "000015_builder_draft_outcomes")
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
	if after := b.send("GET", "/bots/1/chats/1", nil).Body.String(); before != after {
		t.Fatal("forward upgrade changed account/session/Draft/history/accounting")
	}
	for len(requests) > 0 {
		<-requests
	}
	for range 3 {
		if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {strings.Repeat("ب", 32768)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
		if !strings.Contains(page, "تاریخچه اصلی 0") {
			t.Fatal("large-message compaction discarded history")
		}
		for len(requests) > 0 {
			request := <-requests
			if utf8.RuneCountInString(request) > 90000 {
				t.Fatal("summary batch or reply context exceeds fixed character bound")
			}
		}
	}
}

func TestBuilderLongChatKeepsFullHistoryAndDurableIsolatedBoundedMemory(t *testing.T) {
	requests := make(chan string, 64)
	var summaries atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			if strings.Contains(string(body), "متن دوم") {
				memoryReply(w, "یادآوری خصوصی گفتگوی دوم")
				return
			}
			memoryReply(w, "یادآوری خصوصی گفتگوی اول؛ تنظیم قدیمی جایگزین شده است")
			return
		}
		memoryReply(w, "پاسخ محفوظ")
	})
	for turn := range 14 {
		if turn == 7 {
			v := inquiryDraft()
			v.Set("welcome", "پیش نویس دستی authoritative")
			if got := b.post("/bots/1/draft", draftAtRevision(v, "1")); got.Code != 303 {
				t.Fatal(got.Code)
			}
		}
		message := fmt.Sprintf("پیام تاریخی %02d", turn)
		if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {message}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
		for prior := range turn + 1 {
			if !strings.Contains(page, fmt.Sprintf("پیام تاریخی %02d", prior)) {
				t.Fatal("summary removed displayed history")
			}
		}
		for len(requests) > 0 {
			request := <-requests
			if strings.Contains(request, "Summarize older messages") {
				if turn >= 9 && (!strings.Contains(request, "یادآوری خصوصی") || strings.Contains(request, "پیام تاریخی 00")) {
					t.Fatal("restart lost summary boundary or prior memory")
				}
				continue
			}
			var wire struct {
				Messages []json.RawMessage `json:"messages"`
			}
			_ = json.Unmarshal([]byte(request), &wire)
			if len(wire.Messages) > 14 {
				t.Fatal("long conversation model context is unbounded")
			}
			if turn >= 7 && (!strings.Contains(request, "یادآوری خصوصی") || !strings.Contains(request, "پیش نویس دستی authoritative") || !strings.Contains(request, "authoritative") || strings.Contains(request, "پیام تاریخی 00")) {
				t.Fatal("summary, recent history or authoritative current Draft missing")
			}
		}
		if turn == 8 {
			before := page
			a.stopRequests()
			a.builder.Wait()
			_ = a.db.Close()
			restarted, err := New(t.Context(), a.cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
			b.router = restarted.server.Handler
			if after := b.send("GET", "/bots/1/chats/1", nil).Body.String(); after != before {
				t.Fatal("restart changed readable history")
			}
		}
	}
	if summaries.Load() < 2 {
		t.Fatal("older messages were never incrementally summarized")
	}
	for turn := range 8 {
		if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {fmt.Sprintf("متن دوم %d", turn)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		waitBuilder(t, b, "/bots/1/chats/2", "succeeded")
		for len(requests) > 0 {
			request := <-requests
			if strings.Contains(request, "گفتگوی اول") || strings.Contains(request, "پیام تاریخی") {
				t.Fatal("another chat's memory entered context")
			}
			if !strings.Contains(request, "Summarize older messages") && !strings.Contains(request, "پیش نویس دستی authoritative") {
				t.Fatal("chat lost current shared Draft")
			}
			if turn == 7 && !strings.Contains(request, "گفتگوی دوم") {
				t.Fatal("second chat lost its own summary")
			}
		}
	}
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"بازگشت به اول"}})
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	for len(requests) > 0 {
		if request := <-requests; strings.Contains(request, "متن دوم") || strings.Contains(request, "گفتگوی دوم") {
			t.Fatal("second summary entered first chat")
		}
	}
}

func TestBuilderSummaryStorageFailureRollsBackReplyAndDraftAndAllowsRecovery(t *testing.T) {
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Summarize older messages") {
			memoryReply(w, "خلاصه ذخیره نشده")
			return
		}
		if strings.Contains(string(body), "درخواست تغییر") && !strings.Contains(string(body), "tool_calls") {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		memoryReply(w, "پاسخ محفوظ")
	})
	fillMemoryChat(t, b)
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	if _, err := a.db.Exec(`CREATE TRIGGER fail_summary BEFORE INSERT ON builder_summaries BEGIN SELECT RAISE(ABORT,'private storage error'); END`); err != nil {
		t.Fatal(err)
	}
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست تغییر"}})
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if !strings.Contains(page, `data-run-result="memory.failed"`) || strings.Contains(page, "private storage error") || strings.Contains(page, "خلاصه ذخیره نشده") || !strings.Contains(page, "تاریخچه اصلی 0") {
		t.Fatal("summary storage failure was not recoverable")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("summary storage failure applied staged Draft")
	}
	if _, err := a.db.Exec(`DROP TRIGGER fail_summary`); err != nil {
		t.Fatal(err)
	}
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"بازیابی"}})
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
}

func TestBuilderLegacyBacklogMakesProgressAcrossExplicitBudgetLimitedRequests(t *testing.T) {
	requests := make(chan string, 8)
	a, b := builderFixture(t, builder.Config{MaxCalls: 1}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			memoryReply(w, "خلاصه معتبر قبلی")
			return
		}
		memoryReply(w, "پاسخ پس از بازیابی تاریخچه")
	})
	// A pre-summary installation can have more uncovered history than one run
	// can compact. Seed that legacy fixture, then exercise only App HTTP behavior.
	seedLegacyMemoryHistory(t, a)
	for turn, status := range []string{"failed", "failed", "succeeded"} {
		if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه تاریخچه"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := waitBuilder(t, b, "/bots/1/chats/1", status)
		if !strings.Contains(page, "legacy-history-01") || !strings.Contains(page, `data-total-tokens="`+fmt.Sprint((turn+1)*5)+`"`) {
			t.Fatal("backlog recovery lost readable history or accounting")
		}
		request := <-requests
		if turn > 0 && (strings.Contains(request, "legacy-history-01") || !strings.Contains(request, "خلاصه معتبر قبلی")) {
			t.Fatal("budget exhaustion discarded a validated summary boundary")
		}
		if turn == 2 && strings.Contains(request, "Summarize older messages") {
			t.Fatal("explicit requests never cleared legacy backlog")
		}
	}
}

func seedLegacyMemoryHistory(t *testing.T, a *App) {
	t.Helper()
	rollbackToMigration(t, a.db, "000015_builder_draft_outcomes")
	if _, err := a.db.Exec(`WITH RECURSIVE old(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM old WHERE n<30)
INSERT INTO builder_messages (chat_id,sequence,role,content,created_at)
SELECT 1,n,CASE WHEN n%2=1 THEN 'owner' ELSE 'model' END,printf('legacy-history-%02d',n),1 FROM old`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), a.db, false); err != nil {
		t.Fatal(err)
	}
}

func TestBuilderLegacyBacklogRetainsValidatedMemoryAfterLaterBatchTimesOut(t *testing.T) {
	requests := make(chan string, 8)
	var summaries atomic.Int64
	a, b := builderFixture(t, builder.Config{RunTimeout: 300 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			if summaries.Add(1) == 2 {
				<-r.Context().Done()
				return
			}
			memoryReply(w, "خلاصه معتبر پیش از پایان مهلت")
			return
		}
		memoryReply(w, "پاسخ بازیابی شده")
	})
	seedLegacyMemoryHistory(t, a)
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست محدود"}})
	page := waitBuilder(t, b, "/bots/1/chats/1", "timeout")
	if !strings.Contains(page, "legacy-history-01") || !strings.Contains(page, `data-total-tokens="5"`) {
		t.Fatal("timeout lost history or reported usage")
	}
	for len(requests) > 0 {
		<-requests
	}
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"تلاش صریح دوباره"}})
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	for len(requests) > 0 {
		request := <-requests
		if strings.Contains(request, "legacy-history-01") || !strings.Contains(request, "خلاصه معتبر پیش از پایان مهلت") {
			t.Fatal("timeout discarded validated memory progress")
		}
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("timeout recovery changed Draft")
	}
}
