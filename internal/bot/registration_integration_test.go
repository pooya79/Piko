package bot_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestTypedQuestionsAreTemplateAgnosticAndAllowOptionalChoiceAndSignedNumber(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	definition := `{"version":2,"welcome":{"id":"hello","type":"message","text":"سلام"},"menu":{"id":"tasks","type":"menu","text":"منو","choices":[{"id":"custom","label":"دلخواه","target":"application"}]},"messages":[],"forms":[{"id":"application","review":"مرور پاسخ","acknowledgement":"دریافت شد","questions":[{"id":"category","label":"گروه","prompt":"گزینه دلخواه","type":"single_choice","required":false,"options":["الف","ب"]},{"id":"amount","label":"عدد","prompt":"عدد دلخواه","type":"number","required":true,"number":{"min":"-2.5","max":"0"}}]}]}`
	if err := b.SaveDraft(t, 1, url.Values{"definition": {definition}}); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	steps := []url.Values{{"choice": {"custom"}}, {"choice": {"skip"}}, {"answer": {"-۲٫۵"}}, {"choice": {"edit"}}, {"choice": {"edit:amount"}}, {"answer": {"+٠.٠٠"}}, {"choice": {"submit"}}}
	for i, v := range steps {
		v.Set("revision", strconv.Itoa(i+1))
		if got := b.Post(path+"/choose", v); got.Code != 303 {
			t.Fatalf("step %d: %d", i, got.Code)
		}
	}
	page := b.Send("GET", path, nil)
	for _, want := range []string{"بدون پاسخ", "-2.5", "دریافت شد"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
	// The same approved question types validate without a Template identity.
	for _, bad := range []string{strings.Replace(definition, `"options":["الف","ب"]`, `"options":["الف","الف"]`, 1), strings.Replace(definition, `"min":"-2.5"`, `"min":"1"`, 1), strings.Replace(definition, `"type":"single_choice"`, `"type":"short_text"`, 1)} {
		if err := b.SaveDraft(t, 1, url.Values{"definition": {bad}}); err == nil {
			t.Fatal("rejected Draft change was accepted")
		}
	}
}

func TestRegistrationPreviewValidatesChoicesAndExactNumbersAndEditsSummary(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.RegistrationDraft()); err != nil {
		t.Fatal(err)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(v url.Values, status int, want string) {
		t.Helper()
		v.Set("revision", strconv.Itoa(revision))
		if got := b.Post(path+"/choose", v); got.Code != status {
			t.Fatalf("action: %d, want %d", got.Code, status)
		}
		if status == 303 {
			revision++
		}
		if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), want) {
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
		if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), "عدد") {
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
	if got := b.Post(path+"/choose", url.Values{"revision": {strconv.Itoa(revision)}, "choice": {"submit"}}); got.Code != 422 {
		t.Fatal("repeated Preview confirmation accepted")
	}
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created a real Submission")
	}
}
