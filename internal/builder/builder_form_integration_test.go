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

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderFormLimitsAndUnsupportedCapabilitiesPreserveDraft(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*flow.Definition)
	}{
		{"thirteen Questions", func(d *flow.Definition) {
			for i := 6; i < 13; i++ {
				d.Forms[0].Questions = append(d.Forms[0].Questions, flow.Question{ID: fmt.Sprint(i), Label: "نام", Prompt: "نام؟", Type: "short_text"})
			}
		}},
		{"seven destinations", func(d *flow.Definition) {
			for i := 1; i < 7; i++ {
				id := fmt.Sprint(i)
				d.Messages = append(d.Messages, flow.Block{ID: id, Type: "message", Text: "سلام"})
				d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: id, Label: id, Target: id})
			}
		}},
		{"duplicate Question ID", func(d *flow.Definition) { d.Forms[0].Questions[1].ID = "name" }},
		{"duplicate Form ID", func(d *flow.Definition) { d.Forms[0].ID = "welcome" }},
		{"dangling Form reference", func(d *flow.Definition) { d.Menu.Choices[0].Target = "missing" }},
		{"empty Form", func(d *flow.Definition) { d.Forms[0].Questions = nil }},
		{"version one Form", func(d *flow.Definition) { d.Version = 1 }},
		{"invalid short text bound", func(d *flow.Definition) { d.Forms[0].Questions[0].MaxLength = 201 }},
		{"invalid long text bound", func(d *flow.Definition) { d.Forms[0].Questions[1].MaxLength = 2001 }},
		{"reversed number bounds", func(d *flow.Definition) { d.Forms[0].Questions[3].Number.Min = "4" }},
		{"unbounded number exponent", func(d *flow.Definition) { d.Forms[0].Questions[3].Number.Min = "1e1000000" }},
		{"duplicate options", func(d *flow.Definition) { d.Forms[0].Questions[4].Options = []string{"اول", "اول"} }},
		{"reversed date bounds", func(d *flow.Definition) { d.Forms[0].Questions[5].Date.Min = "1405/08/01" }},
		{"invalid Jalali bound", func(d *flow.Definition) { d.Forms[0].Questions[5].Date.Max = "1405/07/31" }},
		{"wrong type rules", func(d *flow.Definition) { d.Forms[0].Questions[2].Number = &flow.NumberRules{Min: "1"} }},
		{"payment", func(d *flow.Definition) { d.Forms[0].Questions[0].Type = "payment" }},
		{"spreadsheet", func(d *flow.Definition) { d.Forms[0].Questions[0].Type = "spreadsheet" }},
		{"arbitrary branching", func(d *flow.Definition) { d.Forms[0].Questions[0].Type = "branch" }},
		{"generated script", func(d *flow.Definition) { d.Forms[0].Questions[0].Type = "script" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d flow.Definition
			if err := json.Unmarshal([]byte(fixture.BuilderFormDraft), &d); err != nil {
				t.Fatal(err)
			}
			tc.change(&d)
			data, _ := json.Marshal(d)
			var calls atomic.Int64
			_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
				} else {
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), "draft.error.") {
						t.Error("recoverable validation feedback missing")
					}
					fixture.BuilderTextReply(w)
				}
			})
			before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"فرم را تغییر بده"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
			if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid Form changed saved Draft")
			}
		})
	}
}

func TestBuilderRepairsFormWithinBudgetOrPreservesOldDraft(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		budget       int64
		fail         bool
	}{
		{"repaired", "succeeded", 3, false},
		{"exhausted after repair", "failed", 2, false},
		{"provider failed during repair", "failed", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			_, b := fixture.BuilderFixture(t, builder.Config{MaxCalls: tc.budget}, func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(fixture.BuilderFormDraft, `"max_length":5`, `"max_length":201`, 1)})
				case 2:
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), "draft.error.definition") {
						t.Error("validation feedback missing")
					}
					if tc.fail {
						w.WriteHeader(500)
						return
					}
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
				case 3:
					fixture.BuilderTextReply(w)
				}
			})
			before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"فرم با حدود معتبر بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", tc.status)
			after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
			if tc.status == "succeeded" {
				if after.Get("draft_revision") != "2" || !strings.Contains(page, `data-total-tokens="17"`) {
					t.Fatal("repaired Form was not saved once with accounted repair calls")
				}
			} else if !reflect.DeepEqual(before, after) {
				t.Fatal("failed or exhausted repair changed Draft")
			}
		})
	}
}

func TestBuilderFailedFormValidationDiscardsEarlierStagedCandidate(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
		case 2:
			fixture.BuilderToolReply(w, "validate_draft", map[string]string{"definition": `{}`})
		case 3:
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), "draft.error.definition") {
				t.Error("repair feedback missing")
			}
			fixture.BuilderTextReply(w)
		}
	})
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"فرم را بساز و بازبینی کن"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if !strings.Contains(page, `data-run-result="invalid"`) {
		t.Fatal("failed repair reported success")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("failed repair applied an earlier partial candidate")
	}
}

func TestBuilderCreatesEveryApprovedQuestionAndPreviewValidatesReviewsAndConfirms(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
		} else {
			fixture.BuilderTextReply(w)
		}
	})
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"فرمی با همه انواع پرسش بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-after-revision="2"`) {
		t.Fatal("generated Form was not atomically saved")
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	step := func(values url.Values, want string) { fixture.BuilderPreviewStep(t, b, path, &revision, values, want) }
	step(url.Values{"choice": {"collect"}}, "نام شما؟")
	if got := b.Post(path+"/choose", url.Values{"choice": {"skip"}, "revision": {"2"}}); got.Code != 422 {
		t.Fatal("required question was skipped")
	}
	step(url.Values{"answer": {"نام بسیار بلند"}}, "بیش از اندازه")
	step(url.Values{"answer": {"مینا"}}, "توضیح شما؟")
	step(url.Values{"answer": {strings.Repeat("س", 11)}}, "بیش از اندازه")
	step(url.Values{"choice": {"skip"}}, "شماره تماس؟")
	step(url.Values{"answer": {"bad phone"}}, "شماره تلفن معتبر")
	step(url.Values{"answer": {"۰۹۱۲۳۴۵۶۷۸۹"}}, "مقدار درخواستی؟")
	step(url.Values{"answer": {"۱٫۴"}}, "دست\u200cکم")
	step(url.Values{"answer": {"۳٫۱"}}, "حداکثر")
	step(url.Values{"answer": {"۲٫۵"}}, "خدمت دلخواه؟")
	step(url.Values{"answer": {"سوم"}}, "گزینه")
	step(url.Values{"choice": {"option:1"}}, "روز ترجیحی؟")
	step(url.Values{"answer": {"1405/07/31"}}, "تاریخ شمسی معتبر")
	step(url.Values{"answer": {"1405/06/31"}}, "تاریخ باید از")
	step(url.Values{"answer": {"۱۴۰۵/۰۷/۱۱"}}, "پاسخ\u200cها را بررسی کنید")
	step(url.Values{"choice": {"edit"}}, "کدام پاسخ")
	step(url.Values{"choice": {"edit:details"}}, "توضیح شما؟")
	step(url.Values{"answer": {"توضیح تازه"}}, "توضیح تازه")
	step(url.Values{"choice": {"submit"}}, "درخواست دریافت شد")
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("generated Preview wrote a real Submission")
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") || !strings.Contains(page, "هنوز به تلگرام") {
		t.Fatal("generation or Preview published or activated Telegram")
	}
}

func TestBuilderCustomizesApprovedTemplatesAndPreviewsTheirRequestSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, secondPrompt, secondAnswer, acknowledgement string
	}{
		{"inquiry", "شماره تماس خود را وارد کنید", "09123456789", "درخواست شما دریافت شد"},
		{"registration", "کدام رویداد یا خدمت", "دوره پیشرفته", "پذیرش یا ظرفیت تضمین"},
		{"booking", "تاریخ ترجیحی شمسی", "۱۴۰۵/۰۷/۱۱", "رزرو قطعی نیست"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					fixture.BuilderToolReply(w, "read_templates", map[string]any{})
				case 2:
					var request struct {
						Messages []struct {
							Role    string
							Content json.RawMessage
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					var templates []struct {
						Name       string
						Definition map[string]any
					}
					for _, m := range request.Messages {
						if m.Role == "tool" {
							var content string
							_ = json.Unmarshal(m.Content, &content)
							_ = json.Unmarshal([]byte(content), &templates)
						}
					}
					for _, template := range templates {
						if template.Name != tc.name {
							continue
						}
						questions := template.Definition["forms"].([]any)[0].(map[string]any)["questions"].([]any)
						questions[0].(map[string]any)["prompt"] = "نام برای درخواست تازه؟"
						questions[2].(map[string]any)["required"] = false
						data, _ := json.Marshal(template.Definition)
						fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
						return
					}
					t.Error("approved Template missing")
					w.WriteHeader(500)
				case 3:
					fixture.BuilderTextReply(w)
				default:
					w.WriteHeader(500)
				}
			})
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"قالب را با پرسش نام تازه بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
			revision := 1
			step := func(values url.Values, want string) { fixture.BuilderPreviewStep(t, b, path, &revision, values, want) }
			step(url.Values{"choice": {tc.name}}, "نام برای درخواست تازه؟")
			step(url.Values{"answer": {"مینا"}}, tc.secondPrompt)
			step(url.Values{"answer": {tc.secondAnswer}}, "رد کردن")
			step(url.Values{"choice": {"skip"}}, "بدون پاسخ")
			step(url.Values{"choice": {"submit"}}, tc.acknowledgement)
			if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
				t.Fatal("Template Preview wrote a Submission")
			}
		})
	}
}

func TestBuilderAddsReordersChangesAndRemovesFormsAcrossIsolatedChats(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1, 4:
			fixture.BuilderToolReply(w, "read_draft", map[string]any{})
		case 2, 5:
			snapshot := fixture.ProviderDraft(t, r)
			data, _ := json.Marshal(snapshot)
			var d flow.Definition
			if err := json.Unmarshal(data, &d); err != nil || len(d.Forms) == 0 {
				t.Error("current authorized Forms missing")
				w.WriteHeader(500)
				return
			}
			if calls.Load() == 2 {
				q := d.Forms[0].Questions
				q[0].Prompt = "نام تغییر یافته؟"
				// Remove long text, move date first, and add an optional Question.
				d.Forms[0].Questions = []flow.Question{q[5], q[0], q[2], q[3], q[4], {ID: "extra", Label: "یادداشت", Prompt: "یادداشت؟", Type: "short_text"}}
				d.Forms = append(d.Forms, flow.Form{ID: "second", Review: "مرور تازه", Acknowledgement: "درخواست تازه دریافت شد", Questions: []flow.Question{{ID: "name", Label: "نام", Prompt: "نام فرم تازه؟", Type: "short_text", Required: true}}})
				d.Menu.Choices = append([]flow.Choice{{ID: "second", Label: "درخواست تازه", Target: "second"}}, d.Menu.Choices...)
			} else {
				body, _ := json.Marshal(snapshot)
				if !strings.Contains(string(body), "نام تغییر یافته؟") {
					t.Error("second chat did not see the current shared Draft")
				}
				d.Forms = d.Forms[1:]
				d.Menu.Choices = d.Menu.Choices[:1]
			}
			data, _ = json.Marshal(d)
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": string(data)})
		case 3, 6:
			fixture.BuilderTextReply(w)
		}
	})
	if got := b.PostDraft(t, "/bots/1/draft", url.Values{"definition": {fixture.BuilderFormDraft}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, chat := range []string{"1", "2"} {
		path := "/bots/1/chats/" + chat
		if got := b.Post(path+"/messages", url.Values{"message": {"فرم\u200cها و ترتیب پرسش\u200cها را تغییر بده"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, path, "succeeded")
		draft := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
		preview := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
		revision := 1
		if chat == "1" {
			if !reflect.DeepEqual(draft["form_id"], []string{"second", "custom"}) || !reflect.DeepEqual(draft["question_id"], []string{"name", "date", "name", "phone", "quantity", "service", "extra"}) {
				t.Fatal("Form/menu or Question arrangement was not applied")
			}
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"collect"}}, "روز ترجیحی؟")
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"answer": {"1405/07/11"}}, "نام تغییر یافته؟")
		} else {
			if !reflect.DeepEqual(draft["form_id"], []string{"second"}) || len(draft["question_id"]) != 1 || draft.Get("draft_revision") != "4" {
				t.Fatal("removed Form or its menu destination remained")
			}
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"second"}}, "نام فرم تازه؟")
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"answer": {"مینا"}}, "مرور تازه")
			fixture.BuilderPreviewStep(t, b, preview, &revision, url.Values{"choice": {"submit"}}, "درخواست تازه دریافت شد")
		}
	}
}
