package builder_test

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

	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
	"github.com/tiktoken-go/tokenizer"
)

func TestBuilderSummaryFailurePreservesHistoryDraftAndRetriesHonestly(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		code       int
	}{
		{"provider", "", 500}, {"empty", " ", 200}, {"oversized", "oversized-private-summary" + strings.Repeat(" x", 2049), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int64
			requests := make(chan string, 4)
			_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "Summarize older messages") {
					requests <- string(body)
					if attempts.Add(1) == 1 {
						if tc.code != 200 {
							w.WriteHeader(tc.code)
							_, _ = w.Write([]byte(`{"error":{"message":"test-server-key private failure"},"usage":{"total_tokens":5,"cost":0.001}}`))
							return
						}
						fixture.MemoryReply(w, tc.text)
						return
					}
					fixture.MemoryReply(w, "خلاصه بازیابی شده")
					return
				}
				fixture.MemoryReply(w, "پاسخ محفوظ")
			})
			fixture.FillMemoryChat(t, b)
			before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
			b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست پس از تاریخچه"}})
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
			if !strings.Contains(page, `data-run-result="memory.failed"`) || !strings.Contains(page, "تاریخچهٔ کامل") || !strings.Contains(page, "تاریخچه اصلی 0") || !strings.Contains(page, `data-admitted="7"`) || !strings.Contains(page, `data-total-tokens="35"`) || strings.Contains(page, "test-server-key") || strings.Contains(page, tc.text) && tc.name == "oversized" {
				t.Fatal("summary failure lost history/accounting or exposed content")
			}
			if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
				t.Fatal("summary failure changed Draft")
			}
			b.Post("/bots/1/chats/1/messages", url.Values{"message": {"تلاش تازه"}})
			fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			for range 2 {
				if request := <-requests; !strings.Contains(request, "تاریخچه اصلی 0") {
					t.Fatal("failed memory advanced boundary")
				}
			}
		})
	}
}

func TestBuilderSummarySharesModelBudgetAndRejectedDeletionPreservesAccounting(t *testing.T) {
	var summaries, calls atomic.Int64
	requests := make(chan string, 32)
	_, b := fixture.BuilderFixture(t, builder.Config{MaxCalls: 1, DailyRequests: 8}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		calls.Add(1)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			fixture.MemoryReply(w, "خلاصه درست")
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	fixture.FillMemoryChat(t, b)
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست بلند"}})
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if summaries.Load() != 1 || calls.Load() != 7 || !strings.Contains(page, `data-run-result="call.limit"`) || !strings.Contains(page, `data-total-tokens="35"`) || !strings.Contains(page, `data-cost="0.007"`) {
		t.Fatal("summary bypassed budget or local accounting")
	}
	// Valid memory survives a later reply failure, without repeating summary charges.
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه"}})
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	var last string
	for len(requests) > 0 {
		last = <-requests
	}
	if !strings.Contains(last, "خلاصه درست") || strings.Contains(last, "تاریخچه اصلی 0") || summaries.Load() != 1 {
		t.Fatal("successful summary was lost after reply budget failure")
	}
	if got := b.Post("/bots/1/chats/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 404 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/chats/2/messages", url.Values{"message": {"بیش از سهمیه"}}); got.Code != 429 || calls.Load() != 8 {
		t.Fatal("rejected deletion bypassed daily allowance")
	}
	page = b.Send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(page, `data-admitted="8"`) || !strings.Contains(page, `data-total-tokens="40"`) || !strings.Contains(page, `data-cost="0.008"`) {
		t.Fatal("rejected deletion lost usage")
	}
}

func TestBuilderSummaryUsesRunDeadlineAndStopsWithoutBackgroundRetry(t *testing.T) {
	var summaries atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{RunTimeout: 2 * time.Second}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			<-r.Context().Done()
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	fixture.FillMemoryChat(t, b)
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست محدود"}})
	// Join the admitted work before reading its durable result. Repeated full
	// history renders are not needed to test the summary's deadline/cancellation.
	a.Builder.Wait()
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "timeout")
	if summaries.Load() != 1 || !strings.Contains(page, "تاریخچه اصلی 0") || !strings.Contains(page, `data-admitted="7"`) {
		t.Fatal("summary deadline lost history or retried")
	}
}

func TestBuilderMemoryBoundsLargeMessagesAndForwardMigrationPreservesData(t *testing.T) {
	requests := make(chan string, 16)
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			fixture.MemoryReply(w, "خلاصه متن بلند")
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	fixture.FillMemoryChat(t, b)
	before := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	fixture.RollbackToMigration(t, a.DB, "000015_builder_draft_outcomes")
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if after := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); before != after {
		t.Fatal("forward upgrade changed account/session/Draft/history/accounting")
	}
	for len(requests) > 0 {
		<-requests
	}
	for range 3 {
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {strings.Repeat(" x", 16384)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
		if !strings.Contains(page, "تاریخچه اصلی 0") {
			t.Fatal("large-message compaction discarded history")
		}
		for len(requests) > 0 {
			request := <-requests
			assertMemoryWireBudget(t, request)
		}
	}
}

func TestBuilderLongChatKeepsFullHistoryAndDurableIsolatedBoundedMemory(t *testing.T) {
	requests := make(chan string, 64)
	var summaries atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			if strings.Contains(string(body), "متن دوم") {
				fixture.MemoryReply(w, "یادآوری خصوصی گفتگوی دوم")
				return
			}
			fixture.MemoryReply(w, "یادآوری خصوصی گفتگوی اول؛ تنظیم قدیمی جایگزین شده است")
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	for turn := range 14 {
		if turn == 7 {
			v := fixture.InquiryDraft()
			v.Set("welcome", "پیش نویس دستی authoritative")
			if got := b.Post("/bots/1/draft", fixture.DraftAtRevision(v, "1")); got.Code != 303 {
				t.Fatal(got.Code)
			}
		}
		message := fixture.MemoryText(fmt.Sprintf("پیام تاریخی %02d", turn), 13000)
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {message}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
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
			assertMemoryWireBudget(t, request)
			if turn >= 7 && (!strings.Contains(request, "یادآوری خصوصی") || !strings.Contains(request, "پیش نویس دستی authoritative") || !strings.Contains(request, "authoritative") || strings.Contains(request, "پیام تاریخی 00")) {
				t.Fatal("summary, recent history or authoritative current Draft missing")
			}
		}
		if turn == 8 {
			before := page
			a.StopWork()
			a.Builder.Wait()
			_ = a.DB.Close()
			restarted, err := fixture.New(t.Context(), a.Config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
			b.Router = restarted.Handler
			if after := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); after != before {
				t.Fatal("restart changed readable history")
			}
		}
	}
	if summaries.Load() < 2 {
		t.Fatal("older messages were never incrementally summarized")
	}
	for turn := range 8 {
		if got := b.Post("/bots/1/chats/2/messages", url.Values{"message": {fixture.MemoryText(fmt.Sprintf("متن دوم %d", turn), 13000)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, "/bots/1/chats/2", "succeeded")
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
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بازگشت به اول"}})
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	for len(requests) > 0 {
		if request := <-requests; strings.Contains(request, "متن دوم") || strings.Contains(request, "گفتگوی دوم") {
			t.Fatal("second summary entered first chat")
		}
	}
}

func TestBuilderSummaryStorageFailureRollsBackReplyAndDraftAndAllowsRecovery(t *testing.T) {
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Summarize older messages") {
			fixture.MemoryReply(w, "خلاصه ذخیره نشده")
			return
		}
		if strings.Contains(string(body), "درخواست تغییر") && !strings.Contains(string(body), "tool_calls") {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	fixture.FillMemoryChat(t, b)
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_summary BEFORE INSERT ON builder_summaries BEGIN SELECT RAISE(ABORT,'private storage error'); END`); err != nil {
		t.Fatal(err)
	}
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست تغییر"}})
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if !strings.Contains(page, `data-run-result="memory.failed"`) || strings.Contains(page, "private storage error") || strings.Contains(page, "خلاصه ذخیره نشده") || !strings.Contains(page, "تاریخچه اصلی 0") {
		t.Fatal("summary storage failure was not recoverable")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("summary storage failure applied staged Draft")
	}
	if _, err := a.DB.Exec(`DROP TRIGGER fail_summary`); err != nil {
		t.Fatal(err)
	}
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بازیابی"}})
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
}

func TestBuilderLegacyBacklogMakesProgressAcrossExplicitBudgetLimitedRequests(t *testing.T) {
	requests := make(chan string, 8)
	a, b := fixture.BuilderFixture(t, builder.Config{MaxCalls: 1}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			fixture.MemoryReply(w, "خلاصه معتبر قبلی")
			return
		}
		fixture.MemoryReply(w, "پاسخ پس از بازیابی تاریخچه")
	})
	// A pre-summary installation can have more uncovered history than one run
	// can compact. Seed that legacy fixture, then exercise only HTTP HTTP behavior.
	fixture.SeedLegacyMemoryHistory(t, a)
	for turn, status := range []string{"failed", "failed", "succeeded"} {
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه تاریخچه"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", status)
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

func TestBuilderLegacyBacklogRetainsValidatedMemoryAfterLaterBatchTimesOut(t *testing.T) {
	requests := make(chan string, 8)
	var summaries atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{RunTimeout: 2 * time.Second}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			if summaries.Add(1) == 2 {
				<-r.Context().Done()
				return
			}
			fixture.MemoryReply(w, "خلاصه معتبر پیش از پایان مهلت")
			return
		}
		fixture.MemoryReply(w, "پاسخ بازیابی شده")
	})
	fixture.SeedLegacyMemoryHistory(t, a)
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست محدود"}})
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "timeout")
	if !strings.Contains(page, "legacy-history-01") || !strings.Contains(page, `data-total-tokens="5"`) {
		t.Fatal("timeout lost history or reported usage")
	}
	for len(requests) > 0 {
		<-requests
	}
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"تلاش صریح دوباره"}})
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	for len(requests) > 0 {
		request := <-requests
		if strings.Contains(request, "legacy-history-01") || !strings.Contains(request, "خلاصه معتبر پیش از پایان مهلت") {
			t.Fatal("timeout discarded validated memory progress")
		}
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("timeout recovery changed Draft")
	}
}

func assertMemoryWireBudget(t *testing.T, request string) {
	t.Helper()
	var wire struct {
		Messages []struct {
			Role    string
			Content json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(request), &wire); err != nil {
		t.Fatal(err)
	}
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	summary := strings.Contains(request, "Summarize older messages")
	for _, m := range wire.Messages {
		if m.Role == "system" && !summary {
			continue
		} // Draft/tools are a separate budget.
		var text string
		if err := json.Unmarshal(m.Content, &text); err != nil {
			var parts []struct{ Text string }
			if err := json.Unmarshal(m.Content, &parts); err != nil {
				t.Fatal(err)
			}
			for _, part := range parts {
				text += part.Text
			}
		}
		n, err := codec.Count(text)
		if err != nil {
			t.Fatal(err)
		}
		total += n + 4
	}
	if summary {
		total += 2048
	}
	if total >= 64000 {
		t.Fatalf("conversation/summary wire input exceeded 64k: %d", total)
	}
}
