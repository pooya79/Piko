package bot_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestDateQuestionsValidateRealJalaliBoundariesAndNormalizeDigitsInPreview(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	// An arbitrary Form ID proves date behavior belongs to the shared runtime.
	definition := `{"version":2,"welcome":{"id":"hello","type":"message","text":"سلام"},"menu":{"id":"tasks","type":"menu","text":"منو","choices":[{"id":"custom","label":"دلخواه","target":"request"}]},"messages":[],"forms":[{"id":"request","review":"مرور پاسخ","acknowledgement":"دریافت شد","questions":[{"id":"preferred","label":"تاریخ","prompt":"روز دلخواه؟","type":"date","required":true}]}]}`
	if got := b.PostDraft(t, "/bots/1/draft", url.Values{"definition": {definition}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.Post(path+"/choose", url.Values{"choice": {"custom"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	revision := 2
	for _, input := range []string{"1402/12/30", "1404/12/30", "1400/12/30", "1394/12/30", "1403/07/31", "1403/12/31", "1403/13/01", "1403/00/01", "1403/01/00", "1403/01/32", "0000/01/01", "3178/01/01", "1403/1", "1403/1/1/1", "1403/01/01T12:00:00+03:30", "1403-01/01", "+1403/01/01", "1e3/01/01", "۱۴۰۳/۰۱/۱٫۵", strings.Repeat("9", 201)} {
		t.Run(input, func(t *testing.T) {
			if got := b.Post(path+"/choose", url.Values{"answer": {input}, "revision": {strconv.Itoa(revision)}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			revision++
			page := b.Send("GET", path, nil).Body.String()
			if !strings.Contains(page, "تاریخ شمسی معتبر") || !strings.Contains(page, `name="answer"`) || strings.Contains(page, `value="submit"`) {
				t.Fatal("invalid date advanced progress or omitted feedback")
			}
		})
	}
	for _, tc := range []struct{ input, display string }{
		{" ۱۴۰۳/۱۲/۳۰ ", "۱۴۰۳/۱۲/۳۰"}, {"1399/12/30", "۱۳۹۹/۱۲/۳۰"}, {"1395/12/30", "۱۳۹۵/۱۲/۳۰"}, {"١٤٠٤/١/١", "۱۴۰۴/۰۱/۰۱"}, {"1403-06-31", "۱۴۰۳/۰۶/۳۱"}, {"1403/7/30", "۱۴۰۳/۰۷/۳۰"}, {"1402/12/29", "۱۴۰۲/۱۲/۲۹"},
	} {
		if got := b.Post(path+"/choose", url.Values{"answer": {tc.input}, "revision": {strconv.Itoa(revision)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		revision++
		page := b.Send("GET", path, nil).Body.String()
		if !strings.Contains(page, "تاریخ:\n"+tc.display) || !strings.Contains(page, `value="submit"`) {
			t.Fatalf("valid date %q missing canonical Jalali display %q", tc.input, tc.display)
		}
		if got := b.Post(path+"/choose", url.Values{"choice": {"back"}, "revision": {strconv.Itoa(revision)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		revision++
	}
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview recorded a real Submission")
	}
}

func TestBookingConfigurationSavesCustomQuestionsAndRequestAcknowledgement(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	page := b.Send("GET", "/bots/1/draft?template=booking", nil)
	for _, want := range []string{"قالب درخواست رزرو", "تاریخ شمسی", "رزرو قطعی نیست"} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Booking defaults missing %q: %d", want, page.Code)
		}
	}
	if got := b.PostDraft(t, "/bots/1/draft", fixture.BookingDraft()); got.Code != 303 {
		t.Fatalf("save Booking: %d", got.Code)
	}
	page = b.Send("GET", "/bots/1/draft", nil)
	for _, want := range []string{`value="booking"`, "تاریخ ترجیحی", "تاریخ ترجیحی شما چیست؟", "رزرو قطعی نیست"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("saved Booking settings missing %q", want)
		}
	}
	bad := fixture.BookingDraft()
	bad["question_required"][1] = "maybe"
	if got := b.PostDraft(t, "/bots/1/draft", bad); got.Code != 422 {
		t.Fatal("invalid required setting accepted")
	}
	if got := b.Send("POST", "/bots/1/draft", fixture.BookingDraft()); got.Code != 403 {
		t.Fatal("Booking save bypassed CSRF")
	}
}

func TestBookingPreviewReviewsEditsSkipsAndConfirmsWithoutRealSubmission(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	v := fixture.BookingDraft()
	v["question_required"][1] = "no"
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(values url.Values, want string) {
		t.Helper()
		values.Set("revision", strconv.Itoa(revision))
		if got := b.Post(path+"/choose", values); got.Code != 303 {
			t.Fatalf("step %d: %d", revision, got.Code)
		}
		revision++
		if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing %q", want)
		}
	}
	advance(url.Values{"choice": {"booking"}}, "نام شما چیست؟")
	advance(url.Values{"answer": {"سارا"}}, "تاریخ ترجیحی شما چیست؟")
	advance(url.Values{"choice": {"skip"}}, "جزئیات درخواست چیست؟")
	advance(url.Values{"choice": {"skip"}}, "بدون پاسخ")
	advance(url.Values{"choice": {"edit"}}, "کدام پاسخ")
	advance(url.Values{"choice": {"edit:date"}}, "تاریخ ترجیحی شما چیست؟")
	advance(url.Values{"answer": {"۱۴۰۳/۱۲/۳۰"}}, "تاریخ ترجیحی:\n۱۴۰۳/۱۲/۳۰")
	if got := b.Post(path+"/choose", url.Values{"revision": {"6"}, "answer": {"1404/1/1"}}); got.Code != 409 {
		t.Fatalf("stale date step accepted: %d", got.Code)
	}
	advance(url.Values{"choice": {"submit"}}, "رزرو قطعی نیست")
	if got := b.Post(path+"/choose", url.Values{"revision": {strconv.Itoa(revision)}, "choice": {"submit"}}); got.Code != 422 {
		t.Fatal("repeated Preview confirmation accepted")
	}
	advance(url.Values{"choice": {"again"}}, "انتخاب کنید")
	advance(url.Values{"choice": {"booking"}}, "نام شما چیست؟")
	advance(url.Values{"answer": {"مینا"}}, "تاریخ ترجیحی شما چیست؟")
	advance(url.Values{"choice": {"cancel"}}, "درخواست لغو شد")
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created a real Submission")
	}
}
