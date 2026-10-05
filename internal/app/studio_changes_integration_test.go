package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/builder"
)

func changesPane(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<section id="studio-changes-panel"`)
	if start < 0 {
		t.Fatal("Changes pane missing")
	}
	end := strings.Index(body[start:], "</section>")
	if end < 0 {
		t.Fatal("Changes pane incomplete")
	}
	return body[start : start+end]
}

func changeRunHTML(t *testing.T, pane string, runID int) string {
	t.Helper()
	start := strings.Index(pane, `data-change-run="`+strconv.Itoa(runID)+`"`)
	if start < 0 {
		t.Fatal("Changes run missing")
	}
	end := strings.Index(pane[start:], "</article>")
	if end < 0 {
		t.Fatal("Changes run incomplete")
	}
	return pane[start : start+end]
}

func TestStudioChangesShowScopedQuestionSettingsAndOrdering(t *testing.T) {
	d, err := flow.Decode(builderFormDraft)
	if err != nil {
		t.Fatal(err)
	}
	questions := d.Forms[0].Questions
	questions[0].Prompt = "نام تازه <script>unsafe</script>"
	questions[0].Required = false
	questions[0].MaxLength = 8
	questions[3].Number.Min = "2"
	questions[4].Options = []string{"دوم", "اول"}
	questions[5].Date.Max = "1405/07/29"
	d.Forms[0].Review = "مرور تازه"
	d.Forms[0].Questions = []flow.Question{questions[2], questions[0], questions[3], questions[4], questions[5], {ID: "extra", Label: "تازه", Prompt: "پرسش تازه؟", Type: "short_text"}}
	data, _ := json.Marshal(d)
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
		case 3:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
		default:
			memoryReply(w, "فقط متن خوش\u200cآمد تغییر کرد")
		}
	})
	saveBuilderChange(t, b, "/bots/1/chats/1")
	saveBuilderChange(t, b, "/bots/1/chats/1")
	pane := changesPane(t, studioRequest(b, "GET", "/bots/1/chats/1", nil).Body.String())
	older := changeRunHTML(t, pane, 1)
	if strings.Contains(older, `/runs/1/undo`) || !strings.Contains(older, "درخواست تازه") || !strings.Contains(changeRunHTML(t, pane, 2), `/runs/2/undo`) {
		t.Fatal("Changes does not explain guarded Undo after a newer request in the same chat")
	}
	pane = changeRunHTML(t, pane, 2)
	for _, want := range []string{`data-change-action="reordered"`, `data-change-id="extra"`, `data-change-id="details"`, `data-change-form="custom"`, `data-change-field="required"`, `data-change-field="max_length"`, `data-change-field="number"`, `data-change-field="options"`, `data-change-field="date"`, "مرور تازه", "&lt;script&gt;unsafe&lt;/script&gt;"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("structured committed diff missing %q", want)
		}
	}
	if strings.Contains(pane, "فقط متن خوش\u200cآمد") || strings.Contains(pane, "<script>unsafe") {
		t.Fatal("diff trusted assistant claims or unescaped content")
	}
	// Technical booleans are presented in the owner's UI language.
	if !strings.Contains(pane, ">بله</bdi>") || !strings.Contains(pane, ">خیر</bdi>") {
		t.Fatal("Question settings are not localized")
	}
}

func TestStudioChangesDescribeCommittedDraftInsteadOfAssistantClaims(t *testing.T) {
	_, b := undoFixture(t)
	saveBuilderChange(t, b, "/bots/1/chats/1")
	pane := changesPane(t, studioRequest(b, "GET", "/bots/1/chats/1", nil).Body.String())
	for _, want := range []string{`data-changes-bot="1"`, `data-changes-revision="2"`, `data-change-run="1"`, `data-change-action="added"`, `data-change-action="removed"`, `data-change-action="edited"`, "Open 9 to 5", `action="/bots/1/chats/1/runs/1/undo"`} {
		if !strings.Contains(pane, want) {
			t.Fatalf("committed Changes missing %q", want)
		}
	}
	if strings.Contains(pane, "منو را آماده کردم") {
		t.Fatal("Changes came from assistant prose")
	}
	preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	// A committed Undo returns the new workspace identity immediately.
	got := studioRequest(b, "POST", "/bots/1/chats/1/runs/1/undo", url.Values{})
	pane = changesPane(t, got.Body.String())
	if got.Code != http.StatusOK || !strings.Contains(pane, `data-changes-revision="3"`) || !strings.Contains(pane, `data-change-result="undone"`) || strings.Contains(pane, `/runs/1/undo`) {
		t.Fatal("Undo fragment did not reconcile Changes")
	}
	if body := b.send("GET", preview, nil).Body.String(); !strings.Contains(body, `data-preview-source-revision="2"`) || !strings.Contains(body, `data-preview-stale="true"`) {
		t.Fatal("Undo did not mark the retained Preview stale")
	}
	if body := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(body, "نسخهٔ منتشرشده: ۰") || !strings.Contains(body, "هنوز به تلگرام") {
		t.Fatal("Undo published or activated the Bot")
	}
}

func TestStudioChangesNeverInventSavedChangesForUncommittedOutcomes(t *testing.T) {
	for _, outcome := range []string{"informational", "identical", "failed", "invalid", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			var calls atomic.Int64
			a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if outcome == "informational" {
					memoryReply(w, "همهٔ پرسش\u200cها حذف شدند")
					return
				}
				if n == 1 {
					if outcome == "identical" {
						builderToolReply(w, "read_draft", map[string]any{})
					} else {
						definition := builderFormDraft
						if outcome == "invalid" {
							definition = `{}`
						}
						builderToolReply(w, "prepare_draft", map[string]string{"definition": definition})
					}
				} else if outcome == "failed" {
					w.WriteHeader(500)
				} else if outcome == "identical" && n == 2 {
					data, _ := json.Marshal(providerDraft(t, r))
					builderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
				} else {
					memoryReply(w, "همهٔ پرسش\u200cها حذف شدند")
				}
			})
			if outcome == "rollback" {
				if _, err := a.db.Exec(`CREATE TRIGGER reject_changes BEFORE UPDATE ON builder_runs WHEN NEW.result='saved' BEGIN SELECT RAISE(ABORT,'private failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}})
			status := "failed"
			if outcome == "informational" || outcome == "identical" {
				status = "succeeded"
			}
			pane := changesPane(t, waitBuilder(t, b, "/bots/1/chats/1", status))
			if strings.Contains(pane, `data-change-action=`) || strings.Contains(pane, "همهٔ پرسش\u200cها حذف شدند") {
				t.Fatal("Changes fabricated a diff from an unsaved candidate or prose")
			}
			if outcome != "identical" && (strings.Contains(pane, `/runs/1/undo`) || strings.Contains(pane, `data-change-result="saved"`)) {
				t.Fatal("unsaved outcome offers Undo or claims a save")
			}
			after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if outcome == "identical" {
				before.Set("draft_revision", "2")
				if !strings.Contains(pane, "محتوای پیش\u200cنویس تغییری نکرده") {
					t.Fatal("identical saved snapshot not explained")
				}
			}
			if before.Encode() != after.Encode() {
				t.Fatal("uncommitted outcome damaged the saved Draft")
			}
		})
	}
}

func TestStudioChangesKeepCollectionsAndSameIDQuestionsSeparate(t *testing.T) {
	base := flow.Definition{
		Version: 2,
		Welcome: flow.Block{ID: "welcome", Type: "message", Text: "سلام"},
		Menu: flow.Block{ID: "menu", Type: "menu", Text: "انتخاب", Choices: []flow.Choice{
			{ID: "a", Label: "اول", Target: "a-message"}, {ID: "b", Label: "دوم", Target: "b-message"},
			{ID: "c", Label: "سوم", Target: "c-form"}, {ID: "d", Label: "چهارم", Target: "d-form"},
		}},
		Messages: []flow.Block{{ID: "a-message", Type: "message", Text: "پیام اول"}, {ID: "b-message", Type: "message", Text: "پیام دوم"}},
		Forms: []flow.Form{
			{ID: "c-form", Review: "مرور", Acknowledgement: "دریافت", Questions: []flow.Question{{ID: "name", Label: "نام اول", Prompt: "پرسش اول", Type: "short_text"}}},
			{ID: "d-form", Review: "مرور", Acknowledgement: "دریافت", Questions: []flow.Question{{ID: "name", Label: "نام دوم", Prompt: "پرسش دوم", Type: "short_text"}}},
		},
	}
	before, _ := json.Marshal(base)
	base.Menu.Choices[0], base.Menu.Choices[1] = base.Menu.Choices[1], base.Menu.Choices[0]
	base.Messages[0], base.Messages[1] = base.Messages[1], base.Messages[0]
	base.Forms[0], base.Forms[1] = base.Forms[1], base.Forms[0]
	base.Forms[0].Questions[0].Prompt = "پرسش دوم تازه"
	after, _ := json.Marshal(base)
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": string(before)})
		case 3:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": string(after)})
		default:
			builderTextReply(w)
		}
	})
	saveBuilderChange(t, b, "/bots/1/chats/1")
	saveBuilderChange(t, b, "/bots/1/chats/1")
	pane := changeRunHTML(t, changesPane(t, studioRequest(b, "GET", "/bots/1/chats/1", nil).Body.String()), 2)
	if strings.Count(pane, `data-change-action="reordered"`) != 3 || strings.Count(pane, `data-change-action="edited"`) != 1 || strings.Contains(pane, `data-change-form="c-form"`) || !strings.Contains(pane, `data-change-form="d-form"`) || !strings.Contains(pane, "پرسش دوم تازه") {
		t.Fatal("collection order or Form-scoped Question diff is wrong")
	}
}
