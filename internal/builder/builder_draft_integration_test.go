package builder_test

import (
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

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderStaleCandidatePreservesManualSaveAndReportsConflict(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fixture.BuilderTextReply(w)
	})
	defer close(release)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ساعت کار را بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("staged run did not request final reply")
	}
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-draft-revision="1"`) {
		t.Fatal("provisional candidate reported as saved")
	}
	manual := fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")
	manual.Set("welcome", "کار تازهٔ مالک")
	if err := b.SaveDraft(t, 1, manual); err != nil {
		t.Fatal(err)
	}
	before := b.LoadDraft(t, 1)
	release <- struct{}{}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if !strings.Contains(page, `data-run-result="conflict"`) || strings.Contains(page, `data-after-revision=`) || strings.Contains(page, "منو را آماده کردم") {
		t.Fatal("stale candidate feedback misleading")
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(after, before) {
		t.Fatal("stale generation overwrote manual save")
	}
}

func TestBuilderInvalidCandidatesCannotChangeDraft(t *testing.T) {
	for _, tc := range []struct{ name, candidate string }{
		{"unsupported version", strings.Replace(fixture.StructuredDraft, `"version":1`, `"version":9`, 1)},
		{"executable field", strings.Replace(fixture.StructuredDraft, `"version":1`, `"version":1,"code":"execute()"`, 1)},
		{"unsupported block", strings.Replace(fixture.StructuredDraft, `"type":"message"`, `"type":"payment"`, 1)},
		{"broken reference", strings.Replace(fixture.StructuredDraft, `"target":"reply"`, `"target":"missing"`, 1)},
		{"duplicate identifier", strings.Replace(fixture.StructuredDraft, `"id":"reply"`, `"id":"welcome"`, 1)},
		{"long identifier", strings.Replace(fixture.StructuredDraft, `"id":"welcome"`, `"id":"`+strings.Repeat("x", 65)+`"`, 1)},
		{"text limit", strings.Replace(fixture.StructuredDraft, `"text":"Hello"`, `"text":"`+strings.Repeat("x", 2001)+`"`, 1)},
		{"label limit", strings.Replace(fixture.StructuredDraft, `"label":"Hours"`, `"label":"`+strings.Repeat("x", 81)+`"`, 1)},
		{"definition size", fixture.StructuredDraft + strings.Repeat(" ", 128<<10)},
		{"trailing data", fixture.StructuredDraft + `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": tc.candidate})
				} else {
					fixture.BuilderTextReply(w)
				}
			})
			before := b.LoadDraft(t, 1)
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست نامعتبر"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
			if !strings.Contains(page, `data-run-result="invalid"`) {
				t.Fatal("invalid candidate outcome missing")
			}
			if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
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
			a, b := fixture.BuilderFixture(t, config, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
				} else if fault == "provider" {
					w.WriteHeader(500)
				} else if fault == "tool" {
					fixture.BuilderToolReply(w, "unsupported_tool", map[string]any{})
				} else if fault == "truncated" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"incomplete"},"finish_reason":"length"}],"usage":{"total_tokens":7}}`))
				} else {
					fixture.BuilderTextReply(w)
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
				if _, err := a.DB.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			before := b.LoadDraft(t, 1)
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"تغییر اتمی"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
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
			if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
				t.Fatal("failed run partially changed Draft")
			}
		})
	}
}

func TestBuilderEditsAndRemovesManualMenuWithIsolatedChatMemory(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		encoded, _ := json.Marshal(req)
		switch calls.Add(1) {
		case 1:
			if !strings.Contains(string(encoded), "Call 02112345678") {
				t.Error("manual Draft missing from context")
			}
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.EditedMenuDraft})
		case 2, 4:
			fixture.BuilderTextReply(w)
		case 3:
			if !strings.Contains(string(encoded), "۱۰ تا ۱۸") || strings.Contains(string(encoded), "ساعت را عوض کن") {
				t.Error("latest shared Draft or isolated memory missing")
			}
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
		default:
			t.Error("unexpected call")
			w.WriteHeader(500)
		}
	})
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.WelcomeDraft(), "1")); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []struct{ chat, message, revision, welcome string }{
		{"1", "ساعت را عوض کن و پیام تماس را نگه دار", "3", "سلام از گفتگو"},
		{"2", "گزینه تماس را حذف کن و فقط ساعت کار را نگه دار", "4", "Hello"},
	} {
		path := "/bots/1/chats/" + turn.chat
		if got := b.Post(path+"/messages", url.Values{"message": {turn.message}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, path, "succeeded")
		draft := b.LoadDraft(t, 1)
		if draft.Get("draft_revision") != turn.revision || draft.Get("welcome") != turn.welcome {
			t.Fatal("edit not applied")
		}
		if turn.chat == "2" && len(draft["choice_id"]) != 1 {
			t.Fatal("removed menu destination remained")
		}
	}
}

func TestBuilderMessageEditsPreserveManualFormsAndUseAuthorizedReadAndValidation(t *testing.T) {
	var calls atomic.Int64
	var candidate string
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.BuilderToolReply(w, "read_draft", map[string]any{})
		case 2:
			d := fixture.ProviderDraft(t, r)
			if d == nil {
				w.WriteHeader(500)
				return
			}
			d["welcome"].(map[string]any)["text"] = "سلام تازه با فرم محفوظ"
			data, _ := json.Marshal(d)
			candidate = string(data)
			fixture.BuilderToolReply(w, "validate_draft", map[string]string{"definition": candidate})
		case 3:
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": candidate})
		case 4:
			fixture.BuilderTextReply(w)
		default:
			w.WriteHeader(500)
		}
	})
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.InquiryDraft(), "1")); err != nil {
		t.Fatal(err)
	}
	before := b.LoadDraft(t, 1)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"فقط خوش\u200cآمد را تغییر بده"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	after := b.LoadDraft(t, 1)
	before.Set("welcome", "سلام تازه با فرم محفوظ")
	before.Set("draft_revision", "3")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("message edit changed supported Form settings")
	}
	started := b.Post("/bots/1/preview", url.Values{})
	path := started.Header().Get("Location")
	if got := b.Post(path+"/choose", url.Values{"choice": {"inquiry"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), "نام شما چیست") {
		t.Fatal("preserved Form no longer runs")
	}
}

func TestBuilderCreatesWorkingMessageMenuDraftAndSharesItAcrossChats(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		body, _ := json.Marshal(request)
		if !strings.Contains(string(body), "یک منو برای ساعت کار بساز") {
			t.Error("owner request missing from generation")
		}
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
		} else {
			fixture.BuilderTextReply(w)
		}
	})
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"یک منو برای ساعت کار بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-after-revision="2"`) || !strings.Contains(page, `data-before-revision="1"`) || !strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-total-tokens="12"`) {
		t.Fatal("saved outcome/revisions/accounting missing")
	}
	draft := b.LoadDraft(t, 1)
	if draft.Get("draft_revision") != "2" || draft.Get("welcome") != "Hello" {
		t.Fatal("generated Draft was not applied")
	}
	other := b.Send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(other, `data-draft-revision="2"`) || strings.Contains(other, "یک منو برای ساعت کار بساز") {
		t.Fatal("shared Draft or isolated histories broken")
	}
	started := b.Post("/bots/1/preview", url.Values{})
	if started.Code != 303 {
		t.Fatal(started.Code)
	}
	path := started.Header().Get("Location")
	if got := b.Post(path+"/choose", url.Values{"choice": {"hours"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Send("GET", path, nil); !strings.Contains(got.Body.String(), "Open 9 to 5") {
		t.Fatal("generated menu does not work in shared Preview/runtime")
	}
	if got := b.Send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), "نسخهٔ منتشرشده: ۰") || !strings.Contains(got.Body.String(), "هنوز به تلگرام") {
		t.Fatal("generation published or connected the Bot")
	}
}

func TestBuilderCanRepairInvalidCandidateWithinBudget(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{MaxCalls: 3}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": `{}`})
		case 2:
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), "draft.error.definition") {
				t.Error("validation feedback missing for repair")
			}
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
		case 3:
			fixture.BuilderTextReply(w)
		default:
			w.WriteHeader(500)
		}
	})
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"یک منوی معتبر بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-after-revision="2"`) || !strings.Contains(page, `data-total-tokens="17"`) {
		t.Fatal("repair did not apply one atomic change and account for all calls")
	}
}

func TestBuilderRejectsAmbiguousParallelDraftCandidates(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			fixture.BuilderTextReply(w)
			return
		}
		tools := []any{}
		for i, data := range []string{fixture.StructuredDraft, fixture.EditedMenuDraft} {
			args, _ := json.Marshal(map[string]string{"definition": data})
			tools = append(tools, map[string]any{"id": fmt.Sprintf("call_%d", i), "type": "function", "function": map[string]string{"name": "prepare_draft", "arguments": string(args)}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": tools}, "finish_reason": "tool_calls"}}})
	})
	before := b.LoadDraft(t, 1)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"یک منو بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("parallel candidates chose an arbitrary Draft")
	}
}

func TestBuilderSavedDraftOutcomeSurvivesRestartAndHistoryDeletion(t *testing.T) {
	var calls atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
		} else {
			fixture.BuilderTextReply(w)
		}
	})
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ساعت کار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	draft := b.LoadDraft(t, 1)
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
		t.Fatal("restart lost saved Draft outcome or revisions")
	}
	if got := b.Post("/bots/1/chats/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal(got.Code)
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(draft, after) || calls.Load() != 2 {
		t.Fatal("rejected deletion reverted Draft or replayed generation")
	}
}
