package app

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
			_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					data, _ := json.Marshal(tc.draft)
					builderToolReply(w, "prepare_bot", map[string]string{"name": "ربات درخواست", "definition": string(data)})
				case 4, 7:
					builderToolReply(w, "read_draft", map[string]any{})
				case 5, 8:
					data, _ := json.Marshal(providerDraft(t, r))
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
					builderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
				default:
					memoryReply(w, "طرح برای پیش نمایش آماده شد")
				}
			})
			chat := startPikoChat(t, b)
			turn := func(message string) {
				t.Helper()
				if got := b.post(chat+"/messages", url.Values{"message": {message}}); got.Code != 303 {
					t.Fatal(got.Code)
				}
				waitBuilder(t, b, chat, "succeeded")
			}
			turn("یک ربات " + tc.name + " بساز")
			original := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if !reflect.DeepEqual(original["question_id"], []string{tc.draft.Forms[0].Questions[0].ID, tc.draft.Forms[0].Questions[1].ID, tc.draft.Forms[0].Questions[2].ID}) {
				t.Fatal("initial Template not saved")
			}
			turn("پرسش نام را تغییر بده و یادداشت اختیاری را اول اضافه کن")
			added := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if added.Get("draft_revision") != "2" || added["question_id"][0] != "note" || added["question_prompt"][1] != "نام تازه؟" {
				t.Fatal("addition/edit not saved")
			}
			preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
			revision := 1
			builderPreviewStep(t, b, preview, &revision, url.Values{"choice": {tc.name}}, "یادداشت تازه؟")
			turn("یادداشت را حذف کن، پرسش دوم را اول ببر و پرسش آخر را اختیاری کن")
			edited := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if edited.Get("draft_revision") != "3" || !reflect.DeepEqual(edited["question_id"], []string{tc.draft.Forms[0].Questions[1].ID, "name", tc.draft.Forms[0].Questions[2].ID}) {
				t.Fatal("removal/reordering not saved")
			}
			// The old Preview retains its isolated snapshot after another committed edit.
			builderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"skip"}}, "نام تازه؟")
			preview = b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
			revision = 1
			step := func(v url.Values, want string) { builderPreviewStep(t, b, preview, &revision, v, want) }
			step(url.Values{"choice": {tc.name}}, tc.draft.Forms[0].Questions[1].Prompt)
			step(url.Values{"answer": {tc.answer}}, "نام تازه؟")
			step(url.Values{"answer": {"مینا"}}, tc.draft.Forms[0].Questions[2].Prompt)
			step(url.Values{"choice": {"skip"}}, tc.draft.Forms[0].Review)
			step(url.Values{"choice": {"edit"}}, "کدام پاسخ")
			step(url.Values{"choice": {"edit:name"}}, "نام تازه؟")
			step(url.Values{"answer": {"سارا"}}, "سارا")
			step(url.Values{"choice": {"submit"}}, tc.draft.Forms[0].Acknowledgement)
			if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
				t.Fatal("Preview created real Submission")
			}
			if got := b.post(chat+"/runs/3/undo", url.Values{}); got.Code != 303 {
				t.Fatal("converted chat Undo", got.Code)
			}
			restored := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			added.Set("draft_revision", "4")
			if !reflect.DeepEqual(restored, added) {
				t.Fatal("Undo did not restore previous committed Template")
			}
			if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") {
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
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			memoryReply(w, `{"intent":"build"}`)
		case 2:
			builderToolReply(w, "prepare_bot", map[string]string{"name": "شش فرم", "definition": string(data)})
		default:
			memoryReply(w, "طرح آماده شد")
		}
	})
	chat := startPikoChat(t, b)
	if got := b.post(chat+"/messages", url.Values{"message": {"رباتی با شش فرم دوازده پرسشی بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, chat, "succeeded")
	saved := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if len(saved["form_id"]) != 6 || len(saved["question_id"]) != 72 {
		t.Fatal("valid boundary configuration lost")
	}
	for form := range 6 {
		preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
		revision := 1
		builderPreviewStep(t, b, preview, &revision, url.Values{"choice": {fmt.Sprintf("form%d", form)}}, "پرسش 0؟")
		for question := range 12 {
			want := fmt.Sprintf("پرسش %d؟", question+1)
			if question == 11 {
				want = "مرور"
			}
			builderPreviewStep(t, b, preview, &revision, url.Values{"answer": {"پاسخ"}}, want)
		}
		builderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"submit"}}, "درخواست دریافت شد")
	}
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("boundary Preview created Submission")
	}
}

func TestPikoCreatedTemplatesDeployRetryPauseAndPinParticipantVersion(t *testing.T) {
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
			data, _ := json.Marshal(tc.draft)
			changed := tc.draft
			// Copy the Questions so the expected original publication stays independent.
			changed.Forms = append([]flow.Form(nil), tc.draft.Forms...)
			changed.Forms[0].Questions = append([]flow.Question(nil), tc.draft.Forms[0].Questions...)
			changed.Forms[0].Questions[1].Prompt = "پرسش نسخه جدید؟"
			changed.Forms[0].Acknowledgement = "دریافت نسخه جدید"
			updated, _ := json.Marshal(changed)
			var calls atomic.Int64
			a, b, f := generalDeployFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					builderToolReply(w, "prepare_bot", map[string]string{"name": "ربات درخواست", "definition": string(data)})
				case 4:
					builderToolReply(w, "prepare_draft", map[string]string{"definition": string(updated)})
				default:
					memoryReply(w, "طرح آماده شد")
				}
			})
			chat := startPikoChat(t, b)
			if got := b.post(chat+"/messages", url.Values{"message": {"ربات " + tc.name + " بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			waitBuilder(t, b, chat, "succeeded")
			if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 409 || !strings.Contains(got.Body.String(), "/bots/1/connect") {
				t.Fatal("missing connection guidance", got.Code)
			}
			if got := b.post("/bots/1/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			f.mu.Lock()
			f.activationFails = true
			f.mu.Unlock()
			if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
				t.Fatal("publication/activation outcomes conflated", got.Code)
			}
			f.mu.Lock()
			f.activationFails = false
			f.mu.Unlock()
			if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
				t.Fatal("retry republished")
			}
			f.mu.Lock()
			secret := f.secret
			f.mu.Unlock()
			d := &formDriver{t: t, a: a, b: b, f: f, secret: secret, update: 100}
			runDeliveryApp(t, a)
			d.text("/start", 2)
			d.press(tc.draft.Menu.Choices[0].Label, 1)
			if got := b.post(chat+"/messages", url.Values{"message": {"پرسش دوم و پیام دریافت را تغییر بده"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			waitBuilder(t, b, chat, "succeeded")
			if got := b.post("/bots/1/pause", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="2"`) {
				t.Fatal(got.Code)
			}
			if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, `action="/bots/1/resume"`) {
				t.Fatal("Deploy resumed paused Bot")
			}
			d.text("پاسخ در حالت توقف", 1)
			d.countSubmissions(0)
			if got := b.post("/bots/1/resume", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			d.text("مینا", 1)
			sent := waitSent(t, f, d.sent)
			if !strings.Contains(sent[len(sent)-1].Text, tc.draft.Forms[0].Questions[1].Prompt) {
				t.Fatal("unfinished Participant moved to new Flow version", sent[len(sent)-1].Text)
			}
			d.text(tc.answer, 1)
			last := "جزئیات آزمایشی"
			if tc.name == "registration" {
				last = "2"
			}
			d.text(last, 4)
			d.press("ارسال", 1)
			d.countSubmissions(1)
			sent = waitSent(t, f, d.sent)
			if !strings.Contains(sent[len(sent)-1].Text, tc.draft.Forms[0].Acknowledgement) {
				t.Fatal("confirmation lost original publication", sent[len(sent)-1].Text)
			}
			d.press("شروع دوباره", 2)
			d.press(tc.draft.Menu.Choices[0].Label, 1)
			d.text("سارا", 1)
			sent = waitSent(t, f, d.sent)
			if !strings.Contains(sent[len(sent)-1].Text, "پرسش نسخه جدید؟") {
				t.Fatal("new Interaction did not use latest publication")
			}
			if calls.Load() != 5 {
				t.Fatal("lifecycle invoked model")
			}
		})
	}
}

func TestPikoUndoAliasRequiresOwnerPOSTCSRFAndBotAssociation(t *testing.T) {
	_, b := undoFixture(t)
	saveBuilderChange(t, b, "/bots/1/chats/1")
	path := "/chats/1/runs/1/undo"
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.send("GET", path, nil); got.Code != 405 {
		t.Fatal("Undo accepted GET", got.Code)
	}
	if got := b.send("POST", path, url.Values{"csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("Undo lacks CSRF", got.Code)
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("journey-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := other.post(path, url.Values{}); got.Code != 404 {
		t.Fatal("cross-owner Undo", got.Code)
	}
	general := startPikoChat(t, b)
	if got := b.post(general+"/runs/1/undo", url.Values{}); got.Code != 404 {
		t.Fatal("general chat borrowed Bot run", got.Code)
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected Undo changed Draft")
	}
	if got := b.send("POST", path, url.Values{"csrf_token": {b.cookie(auth.CSRFCookie)}}); got.Code != 303 {
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
			_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					builderToolReply(w, "prepare_bot", map[string]string{"name": "ربات", "definition": builderFormDraft})
				case 4:
					builderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(builderFormDraft, `"version":2`, `"version":2,`+field, 1)})
				case 5:
					memoryReply(w, "CLAIM-OF-UNSAVED-SUCCESS")
				default:
					memoryReply(w, "طرح آماده شد")
				}
			})
			chat := startPikoChat(t, b)
			if got := b.post(chat+"/messages", url.Values{"message": {"ربات بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			waitBuilder(t, b, chat, "succeeded")
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.post(chat+"/messages", url.Values{"message": {"قابلیت پشتیبانی نشده اضافه کن"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := waitBuilder(t, b, chat, "failed")
			if !strings.Contains(page, `data-run-result="invalid"`) || strings.Contains(page, "CLAIM-OF-UNSAVED-SUCCESS") {
				t.Fatal("invalid capability retained success claim")
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
				t.Fatal("unsupported capability persisted")
			}
		})
	}
}
