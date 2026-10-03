package app

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func registrationDraft() url.Values {
	v := url.Values{"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"}, "form_label": {"درخواست ثبت\u200cنام"}, "review_message": {"پاسخ\u200cها را بررسی کنید"}, "acknowledgement": {"درخواست ثبت\u200cنام دریافت شد؛ پذیرش یا ظرفیت تضمین نمی\u200cشود."}, "question_label": {"نام", "دوره", "مقدار"}, "question_prompt": {"نام شما چیست؟", "کدام دوره؟", "چه مقدار؟"}, "question_required": {"yes", "yes", "no"}}
	v["form_id"] = []string{"registration"}
	v["form_choice_id"] = []string{"registration"}
	v["question_form"] = []string{"registration", "registration", "registration"}
	v["question_id"] = []string{"name", "service", "quantity"}
	v["question_type"] = []string{"short_text", "single_choice", "number"}
	v["question_options"] = []string{"", "هنر\nعلوم", ""}
	v["number_min"] = []string{"", "", "۰٫۱"}
	v["number_max"] = []string{"", "", "9007199254740993.1234567890123456789"}
	v["text_max"] = []string{"", "", ""}
	v["date_min"] = []string{"", "", ""}
	v["date_max"] = []string{"", "", ""}
	return v
}

func TestRegistrationTelegramReviewsEditsAndStoresExactAnswersOncePerAttempt(t *testing.T) {
	d := newFormDriver(t, registrationDraft())
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست ثبت\u200cنام", 1)
	d.text("<b>مینا</b>", 1)
	oldChoice := d.button("هنر")
	d.text("انتخاب نادرست", 2)
	d.press("هنر", 1)
	// A button from the choice question cannot answer the numeric step.
	d.update++
	webhook(d.a, d.secret, callbackPayload(d.update, "old-choice", 77, oldChoice))
	waitAnswers(t, d.f, 3)
	d.text("1e1000000", 2)
	d.text("۹۰۰۷۱۹۹۲۵۴۷۴۰۹۹۳٫۱۲۳۴۵۶۷۸۹۰۱۲۳۴۵۶۷۸۹", 4)
	d.countSubmissions(0)
	d.press("ویرایش پاسخ\u200cها", 1)
	d.press("دوره", 1)
	d.press("علوم", 4)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "مقدار:\n9007199254740993.1234567890123456789" || sent[len(sent)-2].Text != "دوره:\nعلوم" {
		t.Fatal("summary lost edited choice or exact numeric answer")
	}
	confirm := d.button("ارسال")
	d.update++
	webhook(d.a, d.secret, callbackPayload(d.update, "other-participant", 88, confirm))
	waitAnswers(t, d.f, 7)
	d.countSubmissions(0)
	d.press("ارسال", 1)
	if sent := waitSent(t, d.f, d.sent); !strings.Contains(sent[len(sent)-1].Text, "پذیرش یا ظرفیت تضمین نمی\u200cشود") {
		t.Fatal("Registration acknowledgement guarantees acceptance")
	}
	webhook(d.a, d.secret, callbackPayload(d.update, strconv.Itoa(d.update), 77, confirm))
	d.update++
	webhook(d.a, d.secret, callbackPayload(d.update, "repeat-confirm", 77, confirm))
	waitAnswers(t, d.f, 9)
	d.countSubmissions(1)
	page := d.b.send("GET", "/bots/1/submissions/1", nil)
	for _, want := range []string{"&lt;b&gt;مینا&lt;/b&gt;", "علوم", "9007199254740993.1234567890123456789"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("stored answer missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), "<b>مینا</b>") {
		t.Fatal("Submission renders owner answers as markup")
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست ثبت\u200cنام", 1)
	d.text("سارا", 1)
	d.press("علوم", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	other := newAccountBrowser(t, d.a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("registration-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/draft", "/bots/1/preview", "/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.send("GET", path, nil); got.Code != 404 {
			t.Fatalf("other owner accessed %s: %d", path, got.Code)
		}
	}
	if got := other.post("/bots/1/draft", registrationDraft()); got.Code != 404 {
		t.Fatal("cross-owner Registration save")
	}
}

func TestRegistrationConfigurationRejectsInvalidOptionsAndBoundsWithoutChangingDraft(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", registrationDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, tc := range []struct {
		name   string
		change func(url.Values)
	}{
		{"one option", func(v url.Values) { v["question_options"][1] = "هنر" }},
		{"duplicate option", func(v url.Values) { v["question_options"][1] = "هنر\n هنر " }},
		{"empty option", func(v url.Values) { v["question_options"][1] = "هنر\n\nعلوم" }},
		{"too many options", func(v url.Values) { v["question_options"][1] = "1\n2\n3\n4\n5\n6\n7" }},
		{"long option", func(v url.Values) { v["question_options"][1] = strings.Repeat("س", 81) + "\nعلوم" }},
		{"missing options", func(v url.Values) { v.Del("question_options") }},
		{"invalid minimum", func(v url.Values) { v["number_min"][2] = "NaN" }},
		{"exponent maximum", func(v url.Values) { v["number_max"][2] = "1e1000000000" }},
		{"reversed bounds", func(v url.Values) { v["number_min"][2] = "1.00000000000000000001"; v["number_max"][2] = "1" }},
		{"missing bound field", func(v url.Values) { v.Del("number_max") }},
		{"invalid required", func(v url.Values) { v["question_required"][1] = "maybe" }},
		{"missing question", func(v url.Values) { v["question_label"] = v["question_label"][:2] }},
		{"empty acknowledgement", func(v url.Values) { v.Set("acknowledgement", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := registrationDraft()
			tc.change(v)
			if got := b.post("/bots/1/draft", v); got.Code != 422 {
				t.Fatalf("invalid settings accepted: %d", got.Code)
			}
		})
	}
	if page := b.send("GET", "/bots/1/draft", nil); !strings.Contains(page.Body.String(), "هنر\nعلوم") || !strings.Contains(page.Body.String(), "9007199254740993.1234567890123456789") {
		t.Fatal("invalid settings replaced saved Draft")
	}
	if got := b.send("POST", "/bots/1/draft", registrationDraft()); got.Code != 403 {
		t.Fatal("Registration save bypassed CSRF")
	}
}

func TestTypedQuestionsAreTemplateAgnosticAndAllowOptionalChoiceAndSignedNumber(t *testing.T) {
	_, b := draftFixture(t)
	definition := `{"version":2,"welcome":{"id":"hello","type":"message","text":"سلام"},"menu":{"id":"tasks","type":"menu","text":"منو","choices":[{"id":"custom","label":"دلخواه","target":"application"}]},"messages":[],"forms":[{"id":"application","review":"مرور پاسخ","acknowledgement":"دریافت شد","questions":[{"id":"category","label":"گروه","prompt":"گزینه دلخواه","type":"single_choice","required":false,"options":["الف","ب"]},{"id":"amount","label":"عدد","prompt":"عدد دلخواه","type":"number","required":true,"number":{"min":"-2.5","max":"0"}}]}]}`
	if got := b.post("/bots/1/draft", url.Values{"definition": {definition}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	steps := []url.Values{{"choice": {"custom"}}, {"choice": {"skip"}}, {"answer": {"-۲٫۵"}}, {"choice": {"edit"}}, {"choice": {"edit:amount"}}, {"answer": {"+٠.٠٠"}}, {"choice": {"submit"}}}
	for i, v := range steps {
		v.Set("revision", strconv.Itoa(i+1))
		if got := b.post(path+"/choose", v); got.Code != 303 {
			t.Fatalf("step %d: %d", i, got.Code)
		}
	}
	page := b.send("GET", path, nil)
	for _, want := range []string{"بدون پاسخ", "-2.5", "دریافت شد"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
	// The same approved question types validate without a Template identity.
	for _, bad := range []string{strings.Replace(definition, `"options":["الف","ب"]`, `"options":["الف","الف"]`, 1), strings.Replace(definition, `"min":"-2.5"`, `"min":"1"`, 1), strings.Replace(definition, `"type":"single_choice"`, `"type":"short_text"`, 1)} {
		if got := b.post("/bots/1/draft", url.Values{"definition": {bad}}); got.Code != 422 {
			t.Fatal("invalid structured question accepted")
		}
	}
}

func TestRegistrationPreviewValidatesChoicesAndExactNumbersAndEditsSummary(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", registrationDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(v url.Values, status int, want string) {
		t.Helper()
		v.Set("revision", strconv.Itoa(revision))
		if got := b.post(path+"/choose", v); got.Code != status {
			t.Fatalf("action: %d, want %d", got.Code, status)
		}
		if status == 303 {
			revision++
		}
		if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing %q", want)
		}
	}
	advance(url.Values{"choice": {"registration"}}, 303, "نام شما چیست؟")
	advance(url.Values{"answer": {"مینا"}}, 303, "هنر")
	advance(url.Values{"choice": {"option:99"}}, 422, "کدام دوره؟")
	advance(url.Values{"answer": {"گزینه ناشناخته"}}, 303, "گزینه\u200cهای همین پرسش")
	advance(url.Values{"choice": {"skip"}}, 422, "کدام دوره؟")
	advance(url.Values{"choice": {"option:0"}}, 303, "چه مقدار؟")
	for _, value := range []string{"NaN", "1e9", "1,000", "۱۲٬۳", "1.2.3", ".5", "1.", "--2", strings.Repeat("9", 201), "0.09999999999999999999", "9007199254740993.123456789012345679"} {
		advance(url.Values{"answer": {value}}, 303, "چه مقدار؟")
		if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), "عدد") {
			t.Fatal("missing numeric feedback")
		}
	}
	advance(url.Values{"answer": {"۹۰۰۷۱۹۹۲۵۴۷۴۰۹۹۳٫۱۲۳۴۵۶۷۸۹۰۱۲۳۴۵۶۷۸۹"}}, 303, "9007199254740993.1234567890123456789")
	advance(url.Values{"choice": {"edit"}}, 303, "کدام پاسخ")
	advance(url.Values{"choice": {"edit:service"}}, 303, "کدام دوره؟")
	advance(url.Values{"choice": {"option:1"}}, 303, "پاسخ\u200cها را بررسی کنید")
	advance(url.Values{"choice": {"edit"}}, 303, "کدام پاسخ")
	advance(url.Values{"choice": {"edit:quantity"}}, 303, "چه مقدار؟")
	advance(url.Values{"answer": {"٠٫١"}}, 303, "0.1")
	advance(url.Values{"choice": {"submit"}}, 303, "پذیرش یا ظرفیت تضمین نمی\u200cشود")
	if got := b.post(path+"/choose", url.Values{"revision": {strconv.Itoa(revision)}, "choice": {"submit"}}); got.Code != 422 {
		t.Fatal("repeated Preview confirmation accepted")
	}
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created a real Submission")
	}
}

func TestRegistrationConfigurationSavesChoicesAndExactBounds(t *testing.T) {
	_, b := draftFixture(t)
	if page := b.send("GET", "/bots/1/draft?template=registration", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "قالب ثبت\u200cنام") {
		t.Fatal("Registration settings unavailable")
	}
	if got := b.post("/bots/1/draft", registrationDraft()); got.Code != 303 {
		t.Fatalf("save Registration: %d", got.Code)
	}
	page := b.send("GET", "/bots/1/draft", nil)
	for _, want := range []string{"هنر\nعلوم", "9007199254740993.1234567890123456789", "درخواست ثبت\u200cنام", `value="registration"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("saved configuration missing %q", want)
		}
	}
}
