package bot_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestInquiryPreviewQuestionValidationAndRequiredSkip(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(values url.Values, status int, want string) {
		t.Helper()
		values.Set("revision", strconv.Itoa(revision))
		if got := b.Post(path+"/choose", values); got.Code != status {
			t.Fatalf("action status: %d", got.Code)
		}
		if status == 303 {
			revision++
		}
		if want != "" {
			if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), want) {
				t.Fatalf("missing %q", want)
			}
		}
	}
	advance(url.Values{"choice": {"inquiry"}}, 303, "نام شما چیست؟")
	advance(url.Values{"choice": {"skip"}}, 422, "")
	advance(url.Values{"answer": {" "}}, 303, "الزامی")
	advance(url.Values{"answer": {strings.Repeat("س", 201)}}, 303, "بیش از اندازه")
	advance(url.Values{"answer": {strings.Repeat("س", 200)}}, 303, "شماره تماس شما چیست؟")
	for _, value := range []string{"123456", "+1234567890123456", "0912abc6789", "++989123456789"} {
		advance(url.Values{"answer": {value}}, 303, "شماره تلفن معتبر")
	}
	advance(url.Values{"answer": {"+٩٨٩١٢٣٤٥٦٧٨٩"}}, 303, "درخواست شما چیست؟")
	advance(url.Values{"answer": {strings.Repeat("😀", 2001)}}, 303, "بیش از اندازه")
	advance(url.Values{"answer": {strings.Repeat("😀", 2000)}}, 303, "پاسخ\u200cها را بررسی کنید")
	advance(url.Values{"choice": {"back"}}, 303, "درخواست شما چیست؟")
	advance(url.Values{"answer": {"درخواست نهایی"}}, 303, "درخواست نهایی")
	advance(url.Values{"choice": {"cancel"}}, 303, "درخواست لغو شد")
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("cancelled Preview persisted")
	}
}

func TestInquiryConfigurationValidationPreservesDraftAndRequiresCSRF(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	if got := b.Send("GET", "/bots/1/draft?template=inquiry", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "قالب درخواست") {
		t.Fatal("default Inquiry configuration unavailable")
	}
	if got := b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, tc := range []struct {
		name   string
		change func(url.Values)
	}{
		{"empty label", func(v url.Values) { v["question_label"][0] = "" }},
		{"prompt too long", func(v url.Values) { v["question_prompt"][0] = strings.Repeat("س", 2001) }},
		{"missing question", func(v url.Values) { v["question_label"] = v["question_label"][:2] }},
		{"invalid required", func(v url.Values) { v["question_required"][0] = "maybe" }},
		{"empty acknowledgement", func(v url.Values) { v.Set("acknowledgement", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture.InquiryDraft()
			tc.change(v)
			if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 422 {
				t.Fatal(got.Code)
			}
		})
	}
	if got := b.Send("GET", "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "نام شما چیست؟") {
		t.Fatal("invalid settings changed saved Draft")
	}
	if got := b.Send("POST", "/bots/1/draft", fixture.InquiryDraft()); got.Code != 403 {
		t.Fatal("Inquiry save bypassed CSRF")
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("inquiry-other@example.test", "Other", "OwnerPassword123"))
	if got := other.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft()); got.Code != 404 {
		t.Fatal("Inquiry save bypassed ownership")
	}
}

func TestInquiryPreviewCollectsValidatesEditsAndConfirmsWithoutRealSubmissions(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft()); got.Code != 303 {
		t.Fatalf("save Inquiry: %d", got.Code)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(values url.Values, expected string) {
		t.Helper()
		values.Set("revision", strconv.Itoa(revision))
		if got := b.Post(path+"/choose", values); got.Code != 303 {
			t.Fatalf("Preview action: %d", got.Code)
		}
		revision++
		if page := b.Send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), expected) {
			t.Fatalf("missing %q", expected)
		}
	}
	advance(url.Values{"choice": {"inquiry"}}, "نام شما چیست؟")
	advance(url.Values{"answer": {"مینا"}}, "شماره تماس شما چیست؟")
	advance(url.Values{"answer": {"not a phone"}}, "شماره تلفن معتبر")
	advance(url.Values{"answer": {"۰۹۱۲۳۴۵۶۷۸۹"}}, "درخواست شما چیست؟")
	advance(url.Values{"choice": {"skip"}}, "پاسخ\u200cها را بررسی کنید")
	advance(url.Values{"choice": {"edit"}}, "کدام پاسخ")
	advance(url.Values{"choice": {"edit:name"}}, "نام شما چیست؟")
	advance(url.Values{"answer": {"سارا"}}, "پاسخ\u200cها را بررسی کنید")
	advance(url.Values{"choice": {"submit"}}, "درخواست شما دریافت شد")
	if page := b.Send("GET", "/bots/1/submissions", nil); page.Code != 200 || strings.Contains(page.Body.String(), "سارا") {
		t.Fatalf("Preview created a real Submission: %d", page.Code)
	}
}
