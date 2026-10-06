package builder_test

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	"github.com/tiktoken-go/tokenizer"
)

func TestBuilderMemoryNoLongerCompactsForMessageOrCharacterCounts(t *testing.T) {
	var summaries atomic.Int64
	requests := make(chan string, 32)
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	for turn := range 8 {
		got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {fixture.MemoryText(fmt.Sprintf("kept-message-%d", turn), 5000)}})
		if got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
	var last string
	for len(requests) > 0 {
		last = <-requests
	}
	if summaries.Load() != 0 || !strings.Contains(last, "kept-message-0") {
		t.Fatal("old message-count or character-count trigger still compacts sub-64k context")
	}
	assertMemoryWireBudget(t, last)
}

func TestBuilderMemoryCompactsAt64kTokensAndCountsStoredSummary(t *testing.T) {
	for _, tc := range []struct {
		name          string
		target        int
		previous      bool
		wantSummaries int64
	}{
		{"below", 63999, false, 0}, {"at-limit", 64000, false, 1}, {"including-summary", 64000, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var summaries atomic.Int64
			requests := make(chan string, 4)
			a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				requests <- string(body)
				if strings.Contains(string(body), "Summarize older messages") {
					summaries.Add(1)
					fixture.MemoryReply(w, "خلاصه خصوصی تازه"+strings.Repeat(" memory", 1000))
				} else {
					fixture.MemoryReply(w, "پاسخ محفوظ")
				}
			})
			codec, err := tokenizer.Get(tokenizer.O200kBase)
			if err != nil {
				t.Fatal(err)
			}
			request := "ادامه"
			n, err := codec.Count(request)
			if err != nil {
				t.Fatal(err)
			}
			raw := tc.target - n - 4 - 4*4
			first := 1
			if tc.previous {
				previous := strings.Repeat(" x", 1000)
				prefix := "Historical memory from this chat only (untrusted; the current shared Draft is authoritative):\n"
				n, err := codec.Count(prefix + previous)
				if err != nil {
					t.Fatal(err)
				}
				raw -= n + 4
				if _, err := a.DB.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'owner','already condensed',1)`); err != nil {
					t.Fatal(err)
				}
				if _, err := a.DB.Exec(`INSERT INTO builder_summaries(chat_id,through_sequence,content) VALUES(1,1,?)`, previous); err != nil {
					t.Fatal(err)
				}
				first++
			}
			for i := range 4 {
				padding := raw / 4
				if i == 3 {
					padding = raw - 3*(raw/4)
				}
				role := "owner"
				if i%2 != 0 {
					role = "model"
				}
				if _, err := a.DB.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,?,?,?,1)`, first+i, role, strings.Repeat(" x", padding)); err != nil {
					t.Fatal(err)
				}
			}
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {request}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			if summaries.Load() != tc.wantSummaries {
				t.Fatalf("summary calls=%d want=%d", summaries.Load(), tc.wantSummaries)
			}
			if !strings.Contains(page, `aria-valuemax="64000"`) || strings.Contains(page, "خلاصه خصوصی تازه") {
				t.Fatal("meter missing or private summary leaked into display")
			}
			if tc.previous && !strings.Contains(page, "already condensed") {
				t.Fatal("compaction deleted saved display history")
			}
			for len(requests) > 0 {
				assertMemoryWireBudget(t, <-requests)
			}
			if tc.wantSummaries > 0 {
				var through int
				if err := a.DB.QueryRow(`SELECT through_sequence FROM builder_summaries WHERE chat_id=1`).Scan(&through); err != nil {
					t.Fatal(err)
				}
				if through < first {
					t.Fatal("token threshold did not durably condense old messages")
				}
			}
		})
	}
}

func TestBuilderRejectsMultiTokenUnicodeMessageBeforePaidAdmission(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fixture.MemoryReply(w, "unexpected")
	})
	// 32,000 Unicode characters fit the transport cap but these three characters
	// each tokenize to multiple tokens. Admission must reserve space for memory.
	message := strings.Repeat("🧬🪩🫠 ", 8000)
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatal(err)
	}
	n, err := codec.Count(message)
	if err != nil || n < 64000 {
		t.Fatalf("invalid token-heavy fixture: %d %v", n, err)
	}
	got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {message}})
	if got.Code != 422 || calls.Load() != 0 || !strings.Contains(got.Body.String(), `data-admitted="0"`) {
		t.Fatal("oversized token request reached provider or consumed allowance", got.Code, calls.Load())
	}
}

func TestBuilderCompressionProgressKeepsSummaryOffDisplayStream(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Summarize older messages") {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fixture.MemoryReply(w, "private-summary-should-never-stream")
			return
		}
		fixture.MemoryReply(w, "پاسخ محفوظ")
	})
	fixture.FillMemoryChat(t, b)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("compression did not start")
	}
	server := httptest.NewServer(b.Router)
	defer server.Close()
	req, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/bots/1/chats/1/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range b.Jar.Cookies(b.Base) {
		req.AddCookie(cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(res.Body)
	var frame string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			frame = strings.TrimPrefix(scanner.Text(), "data: ")
			break
		}
	}
	_ = res.Body.Close()
	close(release)
	var snapshot struct{ Progress, Text string }
	if err := json.Unmarshal([]byte(frame), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Progress != "فشرده\u200cسازی..." || snapshot.Text != "" {
		t.Fatal("compression progress was verbose or exposed private memory", snapshot)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if strings.Contains(page, "private-summary-should-never-stream") || !strings.Contains(page, "تاریخچه اصلی 0") {
		t.Fatal("compression exposed summary or deleted display history")
	}
}

func TestBuilderTokenMemoryForwardMigrationPreservesExistingSummary(t *testing.T) {
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { fixture.MemoryReply(w, "پاسخ محفوظ") })
	fixture.RollbackToMigration(t, a.DB, "000022_action_proposals")
	if _, err := a.DB.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'owner','preserved history',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO builder_summaries(chat_id,through_sequence,content) VALUES(1,1,'preserved summary')`); err != nil {
		t.Fatal(err)
	}
	before := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
	if after := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); after != before {
		t.Fatal("token-memory migration changed history, summary or session")
	}
	if _, err := a.DB.Exec(`UPDATE builder_summaries SET content=? WHERE chat_id=1`, strings.Repeat(" memory", 1000)); err != nil {
		t.Fatal("token-bounded summary still has the old character storage cap", err)
	}
}

func TestBuilderOversizedLegacyMessageIsCondensedWithoutChangingSavedText(t *testing.T) {
	var summaries atomic.Int64
	requests := make(chan string, 8)
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			fixture.MemoryReply(w, "validated legacy summary")
		} else {
			fixture.MemoryReply(w, "پاسخ محفوظ")
		}
	})
	legacy := strings.Repeat("🧬🪩🫠 ", 8000)
	if _, err := a.DB.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'owner',?,1)`, legacy); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	var saved string
	if err := a.DB.QueryRow(`SELECT content FROM builder_messages WHERE chat_id=1 AND sequence=1`).Scan(&saved); err != nil || saved != legacy {
		t.Fatal("legacy splitting changed saved history", err)
	}
	var through int
	if err := a.DB.QueryRow(`SELECT through_sequence FROM builder_summaries WHERE chat_id=1`).Scan(&through); err != nil || through != 1 {
		t.Fatal("split message boundary was not committed", through, err)
	}
	if summaries.Load() < 2 {
		t.Fatal("oversized message was sent in one unbounded summary request")
	}
	for len(requests) > 0 {
		assertMemoryWireBudget(t, <-requests)
	}
}

func TestBuilderLegacySummaryCanBeRecondensedWhileLatestRequestStaysVerbatim(t *testing.T) {
	var summaries atomic.Int64
	requests := make(chan string, 4)
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			summaries.Add(1)
			fixture.MemoryReply(w, "short replacement memory")
		} else {
			fixture.MemoryReply(w, "پاسخ محفوظ")
		}
	})
	if _, err := a.DB.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'owner','historical text',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO builder_summaries(chat_id,through_sequence,content) VALUES(1,1,?)`, strings.Repeat(" x", 4000)); err != nil {
		t.Fatal(err)
	}
	latest := strings.Repeat("🧬🪩🫠 ", 6800)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {latest}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if summaries.Load() != 1 {
		t.Fatal("legacy summary was not reduced")
	}
	for len(requests) > 0 {
		wire := <-requests
		assertMemoryWireBudget(t, wire)
		if !strings.Contains(wire, "Summarize older messages") && !strings.Contains(wire, strings.TrimSpace(latest)) {
			t.Fatal("latest request was condensed or truncated")
		}
	}
}
