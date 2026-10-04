package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
)

// Script the external OpenRouter wire protocol; generation still uses real Genkit.
func builderToolReply(w http.ResponseWriter, name string, input any) {
	args, _ := json.Marshal(input)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
			"id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": string(args)},
		}}}, "finish_reason": "tool_calls",
	}}, "usage": map[string]any{"total_tokens": 5}})
}

func TestBuilderStaleCandidatePreservesManualSaveAndReportsConflict(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		builderTextReply(w)
	})
	defer close(release)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"ساعت کار را بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("staged run did not request final reply")
	}
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-draft-revision="1"`) {
		t.Fatal("provisional candidate reported as saved")
	}
	manual := draftAtRevision(welcomeDraft(), "1")
	manual.Set("welcome", "کار تازهٔ مالک")
	if got := b.post("/bots/1/draft", manual); got.Code != 303 {
		t.Fatal("manual edits locked during generation", got.Code)
	}
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	release <- struct{}{}
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if !strings.Contains(page, `data-run-result="conflict"`) || strings.Contains(page, `data-after-revision=`) || strings.Contains(page, "منو را آماده کردم") {
		t.Fatal("stale candidate feedback misleading")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(after, before) {
		t.Fatal("stale generation overwrote manual save")
	}
}

func TestBuilderInvalidCandidatesCannotChangeDraft(t *testing.T) {
	for _, tc := range []struct{ name, candidate string }{
		{"unsupported version", strings.Replace(structuredDraft, `"version":1`, `"version":9`, 1)},
		{"executable field", strings.Replace(structuredDraft, `"version":1`, `"version":1,"code":"execute()"`, 1)},
		{"unsupported block", strings.Replace(structuredDraft, `"type":"message"`, `"type":"payment"`, 1)},
		{"broken reference", strings.Replace(structuredDraft, `"target":"reply"`, `"target":"missing"`, 1)},
		{"duplicate identifier", strings.Replace(structuredDraft, `"id":"reply"`, `"id":"welcome"`, 1)},
		{"long identifier", strings.Replace(structuredDraft, `"id":"welcome"`, `"id":"`+strings.Repeat("x", 65)+`"`, 1)},
		{"text limit", strings.Replace(structuredDraft, `"text":"Hello"`, `"text":"`+strings.Repeat("x", 2001)+`"`, 1)},
		{"label limit", strings.Replace(structuredDraft, `"label":"Hours"`, `"label":"`+strings.Repeat("x", 81)+`"`, 1)},
		{"definition size", structuredDraft + strings.Repeat(" ", 128<<10)},
		{"trailing data", structuredDraft + `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					builderToolReply(w, "prepare_draft", map[string]string{"definition": tc.candidate})
				} else {
					builderTextReply(w)
				}
			})
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست نامعتبر"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
			if !strings.Contains(page, `data-run-result="invalid"`) {
				t.Fatal("invalid candidate outcome missing")
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid candidate changed saved Draft")
			}
		})
	}
}

func TestBuilderStagedCandidateRollsBackOnFailureAndBudgetExhaustion(t *testing.T) {
	for _, fault := range []string{"provider", "tool", "truncated", "budget", "reply storage", "Draft storage", "outcome storage"} {
		t.Run(fault, func(t *testing.T) {
			var calls atomic.Int64
			config := builder.Config{}
			if fault == "budget" {
				config.MaxCalls = 1
			}
			a, b := builderFixture(t, config, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
				} else if fault == "provider" {
					w.WriteHeader(500)
				} else if fault == "tool" {
					builderToolReply(w, "unsupported_tool", map[string]any{})
				} else if fault == "truncated" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"incomplete"},"finish_reason":"length"}],"usage":{"total_tokens":7}}`))
				} else {
					builderTextReply(w)
				}
			})
			trigger := ""
			switch fault {
			case "reply storage":
				trigger = `CREATE TRIGGER fail_reply BEFORE INSERT ON builder_messages WHEN NEW.role='model' BEGIN SELECT RAISE(ABORT,'private failure'); END`
			case "Draft storage":
				trigger = `CREATE TRIGGER fail_draft BEFORE UPDATE ON bot_drafts BEGIN SELECT RAISE(ABORT,'private failure'); END`
			case "outcome storage":
				trigger = `CREATE TRIGGER fail_outcome BEFORE UPDATE ON builder_runs WHEN NEW.result='saved' BEGIN SELECT RAISE(ABORT,'private failure'); END`
			}
			if trigger != "" {
				if _, err := a.db.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"تغییر اتمی"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
			if fault == "budget" && !strings.Contains(page, `data-run-result="call.limit"`) {
				t.Fatal("budget feedback missing")
			}
			usage := "12"
			if fault == "budget" || fault == "provider" {
				usage = "5"
			} else if fault == "tool" {
				usage = "10"
			}
			if strings.Contains(page, `data-after-revision=`) || strings.Contains(page, "private failure") || strings.Contains(page, "منو را آماده کردم") || !strings.Contains(page, `data-total-tokens="`+usage+`"`) {
				t.Fatal("partial outcome or unsafe failure feedback")
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
				t.Fatal("failed run partially changed Draft")
			}
		})
	}
}

func TestBuilderShutdownPreventsStagedDraftCommit(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		close(started)
		<-r.Context().Done()
	})
	stop := startBuilderApp(t, a)
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"تغییر پیش از توقف"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate not staged")
	}
	stop()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	page := waitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	if strings.Contains(page, `data-after-revision=`) {
		t.Fatal("shutdown applied a candidate")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("shutdown changed saved Draft")
	}
}

const editedMenuDraft = `{"version":1,"welcome":{"id":"welcome","type":"message","text":"سلام از گفتگو"},"menu":{"id":"menu","type":"menu","text":"انتخاب کنید","choices":[{"id":"hours","label":"ساعت تازه","target":"hours-reply"},{"id":"contact","label":"تماس","target":"contact-reply"}]},"messages":[{"id":"hours-reply","type":"message","text":"۱۰ تا ۱۸"},{"id":"contact-reply","type":"message","text":"02112345678"}]}`

func TestBuilderEditsAndRemovesManualMenuWithIsolatedChatMemory(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		encoded, _ := json.Marshal(req)
		switch calls.Add(1) {
		case 1:
			if !strings.Contains(string(encoded), "Call 02112345678") {
				t.Error("manual Draft missing from context")
			}
			builderToolReply(w, "prepare_draft", map[string]string{"definition": editedMenuDraft})
		case 2, 4:
			builderTextReply(w)
		case 3:
			if !strings.Contains(string(encoded), "۱۰ تا ۱۸") || strings.Contains(string(encoded), "ساعت را عوض کن") {
				t.Error("latest shared Draft or isolated memory missing")
			}
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		default:
			t.Error("unexpected call")
			w.WriteHeader(500)
		}
	})
	if got := b.post("/bots/1/draft", draftAtRevision(welcomeDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, turn := range []struct{ chat, message, revision, welcome string }{
		{"1", "ساعت را عوض کن و پیام تماس را نگه دار", "3", "سلام از گفتگو"},
		{"2", "گزینه تماس را حذف کن و فقط ساعت کار را نگه دار", "4", "Hello"},
	} {
		path := "/bots/1/chats/" + turn.chat
		if got := b.post(path+"/messages", url.Values{"message": {turn.message}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		waitBuilder(t, b, path, "succeeded")
		draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
		if draft.Get("draft_revision") != turn.revision || draft.Get("welcome") != turn.welcome {
			t.Fatal("edit not applied")
		}
		if turn.chat == "2" && len(draft["choice_id"]) != 1 {
			t.Fatal("removed menu destination remained")
		}
	}
}

// Read tool responses as an external provider would; never reach into Builder state.
func providerDraft(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
		return nil
	}
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		var out struct {
			Definition map[string]any `json:"definition"`
		}
		var content string
		if json.Unmarshal(m.Content, &content) == nil && json.Unmarshal([]byte(content), &out) == nil && out.Definition != nil {
			return out.Definition
		}
	}
	t.Error("authorized Draft tool response missing")
	return nil
}

func TestBuilderMessageEditsPreserveManualFormsAndUseAuthorizedReadAndValidation(t *testing.T) {
	var calls atomic.Int64
	var candidate string
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			builderToolReply(w, "read_draft", map[string]any{})
		case 2:
			d := providerDraft(t, r)
			if d == nil {
				w.WriteHeader(500)
				return
			}
			d["welcome"].(map[string]any)["text"] = "سلام تازه با فرم محفوظ"
			data, _ := json.Marshal(d)
			candidate = string(data)
			builderToolReply(w, "validate_draft", map[string]string{"definition": candidate})
		case 3:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": candidate})
		case 4:
			builderTextReply(w)
		default:
			w.WriteHeader(500)
		}
	})
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"فقط خوش\u200cآمد را تغییر بده"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	before.Set("welcome", "سلام تازه با فرم محفوظ")
	before.Set("draft_revision", "3")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("message edit changed supported Form settings")
	}
	started := b.post("/bots/1/preview", url.Values{})
	path := started.Header().Get("Location")
	if got := b.post(path+"/choose", url.Values{"choice": {"inquiry"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), "نام شما چیست") {
		t.Fatal("preserved Form no longer runs")
	}
}

func builderTextReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"منو را آماده کردم؛ آن را در پیش\u200cنمایش امتحان کنید."},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`))
}

func TestBuilderCreatesWorkingMessageMenuDraftAndSharesItAcrossChats(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		body, _ := json.Marshal(request)
		if !strings.Contains(string(body), "یک منو برای ساعت کار بساز") {
			t.Error("owner request missing from generation")
		}
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		} else {
			builderTextReply(w)
		}
	})
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"یک منو برای ساعت کار بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-after-revision="2"`) || !strings.Contains(page, `data-before-revision="1"`) || !strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-total-tokens="12"`) {
		t.Fatal("saved outcome/revisions/accounting missing")
	}
	draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if draft.Get("draft_revision") != "2" || draft.Get("welcome") != "Hello" {
		t.Fatal("generated Draft was not applied")
	}
	other := b.send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(other, `data-draft-revision="2"`) || strings.Contains(other, "یک منو برای ساعت کار بساز") {
		t.Fatal("shared Draft or isolated histories broken")
	}
	started := b.post("/bots/1/preview", url.Values{})
	if started.Code != 303 {
		t.Fatal(started.Code)
	}
	path := started.Header().Get("Location")
	if got := b.post(path+"/choose", url.Values{"choice": {"hours"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.send("GET", path, nil); !strings.Contains(got.Body.String(), "Open 9 to 5") {
		t.Fatal("generated menu does not work in shared Preview/runtime")
	}
	if got := b.send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), "نسخهٔ منتشرشده: ۰") || !strings.Contains(got.Body.String(), "هنوز به تلگرام") {
		t.Fatal("generation published or connected the Bot")
	}
}

func TestBuilderCanRepairInvalidCandidateWithinBudget(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{MaxCalls: 3}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": `{}`})
		case 2:
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), "draft.error.definition") {
				t.Error("validation feedback missing for repair")
			}
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		case 3:
			builderTextReply(w)
		default:
			w.WriteHeader(500)
		}
	})
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"یک منوی معتبر بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-after-revision="2"`) || !strings.Contains(page, `data-total-tokens="17"`) {
		t.Fatal("repair did not apply one atomic change and account for all calls")
	}
}

func TestBuilderRejectsAmbiguousParallelDraftCandidates(t *testing.T) {
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			builderTextReply(w)
			return
		}
		tools := []any{}
		for i, data := range []string{structuredDraft, editedMenuDraft} {
			args, _ := json.Marshal(map[string]string{"definition": data})
			tools = append(tools, map[string]any{"id": fmt.Sprintf("call_%d", i), "type": "function", "function": map[string]string{"name": "prepare_draft", "arguments": string(args)}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": tools}, "finish_reason": "tool_calls"}}})
	})
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"یک منو بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("parallel candidates chose an arbitrary Draft")
	}
}

func TestBuilderSavedDraftOutcomeSurvivesRestartAndHistoryDeletion(t *testing.T) {
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		} else {
			builderTextReply(w)
		}
	})
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"ساعت کار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
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
		t.Fatal("restart lost saved Draft outcome or revisions")
	}
	if got := b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(draft, after) || calls.Load() != 2 {
		t.Fatal("history deletion reverted Draft or replayed generation")
	}
}

func TestBuilderDraftOutcomeMigrationPreservesExistingReplyAndBotData(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست پیشین", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پیشرفت پیشین", 1)
	preview := d.b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	draft := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String())
	submission := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String()
	previewBody := d.b.send("GET", preview, nil).Body.String()
	if got := d.b.post("/bots/1/chats", url.Values{"title": {"گفتگوی پیشین"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	stop()
	legacy, err := database.Open(context.Background(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	rollbackToMigration(t, legacy, "000014_builder_background")
	if _, err := legacy.Exec(`INSERT INTO builder_runs(owner_id,bot_id,chat_id,day,model,draft_revision,status,created_at,lease_until,finished_at) VALUES(1,1,1,'2026-10-04','legacy-model',1,'succeeded',1,0,2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'model','پاسخ قدیمی بدون تغییر پیش\u200cنویس',1)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(t.Context(), legacy, false); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	page := d.b.send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(page, "پاسخ قدیمی بدون تغییر") || strings.Contains(page, `data-after-revision=`) || strings.Contains(page, `data-run-result="saved"`) {
		t.Fatal("upgrade fabricated Draft mutation or lost legacy reply")
	}
	if after := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(draft, after) {
		t.Fatal("upgrade changed Draft/account/session/Bot")
	}
	if page := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String(); page != submission {
		t.Fatal("upgrade lost Submission")
	}
	if page := d.b.send("GET", preview, nil).Body.String(); page != previewBody {
		t.Fatal("upgrade lost Preview snapshot")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("upgrade lost credentials, publications or progress")
	}
}
