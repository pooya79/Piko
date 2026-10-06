package builder_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestPikoTemplateJourneysCreateEditPreviewAndUndo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		draft  flow.Definition
		answer string
	}{
		{"inquiry", inquiry.Default().Definition(), "09123456789"},
		{"registration", registration.Default().Definition(), "دوره پیشرفته"},
		{"booking", booking.Default().Definition(), "1405/07/11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					data, _ := json.Marshal(tc.draft)
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ربات درخواست", "definition": string(data)})
				case 4, 7:
					fixture.BuilderToolReply(w, "read_draft", map[string]any{})
				case 5, 8:
					data, _ := json.Marshal(fixture.ProviderDraft(t, r))
					var d flow.Definition
					if err := json.Unmarshal(data, &d); err != nil || len(d.Forms) != 1 {
						t.Error("authorized Template Draft missing")
						w.WriteHeader(500)
						return
					}
					q := d.Forms[0].Questions
					if calls.Load() == 5 {
						q[0].Prompt = "نام تازه؟"
						// Add an optional Question and move it before existing content.
						d.Forms[0].Questions = append([]flow.Question{{ID: "note", Label: "یادداشت", Prompt: "یادداشت تازه؟", Type: "short_text"}}, q...)
					} else {
						// Remove the addition and move the name behind the second Question.
						d.Forms[0].Questions = []flow.Question{q[2], q[1], q[3]}
						d.Forms[0].Questions[2].Required = false
					}
					data, _ = json.Marshal(d)
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
				default:
					fixture.MemoryReply(w, "طرح برای پیش نمایش آماده شد")
				}
			})
			chat := fixture.StartPikoChat(t, b)
			turn := func(message string) {
				t.Helper()
				if got := b.Post(chat+"/messages", url.Values{"message": {message}}); got.Code != 303 {
					t.Fatal(got.Code)
				}
				fixture.WaitBuilder(t, b, chat, "succeeded")
			}
			turn("یک ربات " + tc.name + " بساز")
			original := b.LoadDraft(t, 1)
			if !reflect.DeepEqual(original["question_id"], []string{tc.draft.Forms[0].Questions[0].ID, tc.draft.Forms[0].Questions[1].ID, tc.draft.Forms[0].Questions[2].ID}) {
				t.Fatal("initial Template not saved")
			}
			turn("پرسش نام را تغییر بده و یادداشت اختیاری را اول اضافه کن")
			added := b.LoadDraft(t, 1)
			if added.Get("draft_revision") != "2" || added["question_id"][0] != "note" || added["question_prompt"][1] != "نام تازه؟" {
				t.Fatal("addition/edit not saved")
			}
			preview := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
			revision := 1
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {tc.name}}, "یادداشت تازه؟")
			turn("یادداشت را حذف کن، پرسش دوم را اول ببر و پرسش آخر را اختیاری کن")
			edited := b.LoadDraft(t, 1)
			if edited.Get("draft_revision") != "3" || !reflect.DeepEqual(edited["question_id"], []string{tc.draft.Forms[0].Questions[1].ID, "name", tc.draft.Forms[0].Questions[2].ID}) {
				t.Fatal("removal/reordering not saved")
			}
			// The old Preview retains its isolated snapshot after another committed edit.
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"skip"}}, "نام تازه؟")
			preview = b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
			revision = 1
			step := func(v url.Values, want string) { fixture.BuilderPreviewStep(t, b, preview, &revision, v, want) }
			step(url.Values{"choice": {tc.name}}, tc.draft.Forms[0].Questions[1].Prompt)
			step(url.Values{"answer": {tc.answer}}, "نام تازه؟")
			step(url.Values{"answer": {"مینا"}}, tc.draft.Forms[0].Questions[2].Prompt)
			step(url.Values{"choice": {"skip"}}, tc.draft.Forms[0].Review)
			step(url.Values{"choice": {"edit"}}, "کدام پاسخ")
			step(url.Values{"choice": {"edit:name"}}, "نام تازه؟")
			step(url.Values{"answer": {"سارا"}}, "سارا")
			step(url.Values{"choice": {"submit"}}, tc.draft.Forms[0].Acknowledgement)
			if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
				t.Fatal("Preview created real Submission")
			}
			if got := b.Post(chat+"/runs/3/undo", url.Values{}); got.Code != 303 {
				t.Fatal("converted chat Undo", got.Code)
			}
			restored := b.LoadDraft(t, 1)
			added.Set("draft_revision", "4")
			if !reflect.DeepEqual(restored, added) {
				t.Fatal("Undo did not restore previous committed Template")
			}
			if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") {
				t.Fatal("Draft edit/Preview/Undo published")
			}
			if calls.Load() != 9 {
				t.Fatal("Preview or Undo invoked model")
			}
		})
	}
}

func TestPikoAcceptsMaximumMenuAndFormSizes(t *testing.T) {
	d := inquiry.Default().Definition()
	d.Forms, d.Menu.Choices = nil, nil
	for form := range 6 {
		id := fmt.Sprintf("form%d", form)
		f := flow.Form{ID: id, Review: "مرور", Acknowledgement: "درخواست دریافت شد"}
		for question := range 12 {
			f.Questions = append(f.Questions, flow.Question{ID: fmt.Sprintf("q%d", question), Label: "نام", Prompt: fmt.Sprintf("پرسش %d؟", question), Type: "short_text", Required: true})
		}
		d.Forms = append(d.Forms, f)
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: id, Label: fmt.Sprintf("فرم %d", form), Target: id})
	}
	data, _ := json.Marshal(d)
	var calls atomic.Int64
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 2:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "شش فرم", "definition": string(data)})
		default:
			fixture.MemoryReply(w, "طرح آماده شد")
		}
	})
	chat := fixture.StartPikoChat(t, b)
	if got := b.Post(chat+"/messages", url.Values{"message": {"رباتی با شش فرم دوازده پرسشی بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, chat, "succeeded")
	saved := b.LoadDraft(t, 1)
	if len(saved["form_id"]) != 6 || len(saved["question_id"]) != 72 {
		t.Fatal("valid boundary configuration lost")
	}
	for form := range 6 {
		preview := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
		revision := 1
		fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {fmt.Sprintf("form%d", form)}}, "پرسش 0؟")
		for question := range 12 {
			want := fmt.Sprintf("پرسش %d؟", question+1)
			if question == 11 {
				want = "مرور"
			}
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"answer": {"پاسخ"}}, want)
		}
		fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"submit"}}, "درخواست دریافت شد")
	}
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("boundary Preview created Submission")
	}
}

func TestPikoUndoAliasRequiresOwnerPOSTCSRFAndBotAssociation(t *testing.T) {
	_, b := fixture.UndoFixture(t)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	path := "/chats/1/runs/1/undo"
	before := b.LoadDraft(t, 1)
	if got := b.Send("GET", path, nil); got.Code != 405 {
		t.Fatal("Undo accepted GET", got.Code)
	}
	if got := b.Send("POST", path, url.Values{"csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("Undo lacks CSRF", got.Code)
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("journey-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := other.Post(path, url.Values{}); got.Code != 404 {
		t.Fatal("cross-owner Undo", got.Code)
	}
	general := fixture.StartPikoChat(t, b)
	if got := b.Post(general+"/runs/1/undo", url.Values{}); got.Code != 404 {
		t.Fatal("general chat borrowed Bot run", got.Code)
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected Undo changed Draft")
	}
	if got := b.Send("POST", path, url.Values{"csrf_token": {b.Cookie(auth.CSRFCookie)}}); got.Code != 303 {
		t.Fatal("owner Undo unavailable", got.Code)
	}
}

func TestPikoUnsupportedCandidateFieldsCannotPersistCapabilitiesOrSuccessClaims(t *testing.T) {
	for _, field := range []string{
		`"integration":{"provider":"spreadsheet"}`,
		`"branch":{"condition":"capacity"}`,
		`"reservation":{"guaranteed":true}`,
		`"test_passed":true`,
		`"deployed":true`,
	} {
		t.Run(field, func(t *testing.T) {
			var calls atomic.Int64
			_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ربات", "definition": fixture.BuilderFormDraft})
				case 4:
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(fixture.BuilderFormDraft, `"version":2`, `"version":2,`+field, 1)})
				case 5:
					fixture.MemoryReply(w, "CLAIM-OF-UNSAVED-SUCCESS")
				default:
					fixture.MemoryReply(w, "طرح آماده شد")
				}
			})
			chat := fixture.StartPikoChat(t, b)
			if got := b.Post(chat+"/messages", url.Values{"message": {"ربات بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, chat, "succeeded")
			before := b.LoadDraft(t, 1)
			if got := b.Post(chat+"/messages", url.Values{"message": {"قابلیت پشتیبانی نشده اضافه کن"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := fixture.WaitBuilder(t, b, chat, "failed")
			if !strings.Contains(page, `data-run-result="invalid"`) || strings.Contains(page, "CLAIM-OF-UNSAVED-SUCCESS") {
				t.Fatal("invalid capability retained success claim")
			}
			if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
				t.Fatal("unsupported capability persisted")
			}
		})
	}
}
