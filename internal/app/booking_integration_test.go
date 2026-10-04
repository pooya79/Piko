package app

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
)

func bookingDraft() url.Values {
	v := url.Values{"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"}, "form_label": {"درخواست رزرو"}, "review_message": {"درخواست را بررسی کنید"}, "acknowledgement": {"درخواست شما دریافت شد؛ رزرو قطعی نیست."}, "question_label": {"نام", "تاریخ ترجیحی", "جزئیات"}, "question_prompt": {"نام شما چیست؟", "تاریخ ترجیحی شما چیست؟", "جزئیات درخواست چیست؟"}, "question_required": {"yes", "yes", "no"}}
	v["form_id"] = []string{"booking"}
	v["form_choice_id"] = []string{"booking"}
	v["question_form"] = []string{"booking", "booking", "booking"}
	v["question_id"] = []string{"name", "date", "details"}
	v["question_type"] = []string{"short_text", "date", "long_text"}
	v["question_options"] = []string{"", "", ""}
	v["number_min"] = []string{"", "", ""}
	v["number_max"] = []string{"", "", ""}
	v["text_max"] = []string{"", "", ""}
	v["date_min"] = []string{"", "", ""}
	v["date_max"] = []string{"", "", ""}
	return v
}

func TestBookingTelegramEditsDatesAndStoresOneRequestPerAttempt(t *testing.T) {
	d := newFormDriver(t, bookingDraft())
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست رزرو", 1)
	d.text("<b>مینا</b>", 1)
	d.text("۱۴۰۴/۱۲/۳۰", 2)
	sent := waitSent(t, d.f, d.sent)
	if !strings.Contains(sent[len(sent)-2].Text, "تاریخ شمسی معتبر") {
		t.Fatal("invalid Telegram date omitted feedback")
	}
	d.text("۱۴۰۳/۱۲/۳۰", 1)
	d.text("اتاق برای دو نفر", 4)
	d.countSubmissions(0)
	d.press("ویرایش پاسخ\u200cها", 1)
	d.press("تاریخ ترجیحی", 1)
	d.text("١٤٠٤/١/١", 4)
	sent = waitSent(t, d.f, d.sent)
	if sent[len(sent)-2].Text != "تاریخ ترجیحی:\n۱۴۰۴/۰۱/۰۱" {
		t.Fatal("Telegram review lost edited Jalali date")
	}
	confirm := d.button("ارسال")
	d.update++
	webhook(d.a, d.secret, callbackPayload(d.update, "foreign-confirm", 88, confirm))
	waitAnswers(t, d.f, 4)
	d.countSubmissions(0)
	d.press("ارسال", 1)
	if sent := waitSent(t, d.f, d.sent); !strings.Contains(sent[len(sent)-1].Text, "رزرو قطعی نیست") {
		t.Fatal("Booking acknowledgement omitted request semantics")
	}
	webhook(d.a, d.secret, callbackPayload(d.update, strconv.Itoa(d.update), 77, confirm))
	d.update++
	webhook(d.a, d.secret, callbackPayload(d.update, "repeat-confirm", 77, confirm))
	waitAnswers(t, d.f, 6)
	d.countSubmissions(1)
	page := d.b.send("GET", "/bots/1/submissions/1", nil)
	for _, want := range []string{"&lt;b&gt;مینا&lt;/b&gt;", "۱۴۰۴/۰۱/۰۱", "اتاق برای دو نفر"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("owner-visible answer missing %q", want)
		}
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست رزرو", 1)
	d.text("سارا", 1)
	d.text("1405-07-11", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if page := d.b.send("GET", "/bots/1/submissions/2", nil); !strings.Contains(page.Body.String(), "۱۴۰۵/۰۷/۱۱") || !strings.Contains(page.Body.String(), "بدون پاسخ") {
		t.Fatal("second request lost normalized date or skipped details")
	}
	other := newAccountBrowser(t, d.a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("booking-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/draft?template=booking", "/bots/1/preview", "/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.send("GET", path, nil); got.Code != 404 {
			t.Fatalf("other owner accessed %s: %d", path, got.Code)
		}
	}
	if got := other.postDraft(t, "/bots/1/draft", bookingDraft()); got.Code != 404 {
		t.Fatal("cross-owner Booking save")
	}
}

func TestBookingRetainsDateQuestionAndAnswersAcrossPublicationAndRestart(t *testing.T) {
	d := newFormDriver(t, bookingDraft())
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست رزرو", 1)
	d.text("مینا", 1)
	changed := bookingDraft()
	changed["question_prompt"][1] = "تاریخ تازه؟"
	changed["question_label"][1] = "تاریخ تازه"
	changed.Set("acknowledgement", "درخواست تازه دریافت شد")
	if got := d.b.postDraft(t, "/bots/1/draft", changed); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.sent)
	if !strings.Contains(sent[len(sent)-1].Text, "تاریخ ترجیحی شما چیست؟") || strings.Contains(sent[len(sent)-1].Text, "تاریخ تازه؟") {
		t.Fatal("unfinished Booking switched Flow version")
	}
	d.text("۱۴۰۳/۱۲/۳۰", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	page := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String()
	if !strings.Contains(page, "تاریخ ترجیحی") || !strings.Contains(page, "۱۴۰۳/۱۲/۳۰") || strings.Contains(page, "تاریخ تازه") {
		t.Fatal("stored date or frozen label lost across restart")
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست رزرو", 1)
	d.text("سارا", 1)
	sent = waitSent(t, d.f, d.sent)
	if !strings.Contains(sent[len(sent)-1].Text, "تاریخ تازه؟") {
		t.Fatal("new Booking did not select latest published version")
	}
}

func TestDateQuestionsValidateRealJalaliBoundariesAndNormalizeDigitsInPreview(t *testing.T) {
	_, b := draftFixture(t)
	// An arbitrary Form ID proves date behavior belongs to the shared runtime.
	definition := `{"version":2,"welcome":{"id":"hello","type":"message","text":"سلام"},"menu":{"id":"tasks","type":"menu","text":"منو","choices":[{"id":"custom","label":"دلخواه","target":"request"}]},"messages":[],"forms":[{"id":"request","review":"مرور پاسخ","acknowledgement":"دریافت شد","questions":[{"id":"preferred","label":"تاریخ","prompt":"روز دلخواه؟","type":"date","required":true}]}]}`
	if got := b.postDraft(t, "/bots/1/draft", url.Values{"definition": {definition}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.post(path+"/choose", url.Values{"choice": {"custom"}, "revision": {"1"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	revision := 2
	for _, input := range []string{"1402/12/30", "1404/12/30", "1400/12/30", "1394/12/30", "1403/07/31", "1403/12/31", "1403/13/01", "1403/00/01", "1403/01/00", "1403/01/32", "0000/01/01", "3178/01/01", "1403/1", "1403/1/1/1", "1403/01/01T12:00:00+03:30", "1403-01/01", "+1403/01/01", "1e3/01/01", "۱۴۰۳/۰۱/۱٫۵", strings.Repeat("9", 201)} {
		t.Run(input, func(t *testing.T) {
			if got := b.post(path+"/choose", url.Values{"answer": {input}, "revision": {strconv.Itoa(revision)}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			revision++
			page := b.send("GET", path, nil).Body.String()
			if !strings.Contains(page, "تاریخ شمسی معتبر") || !strings.Contains(page, `name="answer"`) || strings.Contains(page, `value="submit"`) {
				t.Fatal("invalid date advanced progress or omitted feedback")
			}
		})
	}
	for _, tc := range []struct{ input, display string }{
		{" ۱۴۰۳/۱۲/۳۰ ", "۱۴۰۳/۱۲/۳۰"}, {"1399/12/30", "۱۳۹۹/۱۲/۳۰"}, {"1395/12/30", "۱۳۹۵/۱۲/۳۰"}, {"١٤٠٤/١/١", "۱۴۰۴/۰۱/۰۱"}, {"1403-06-31", "۱۴۰۳/۰۶/۳۱"}, {"1403/7/30", "۱۴۰۳/۰۷/۳۰"}, {"1402/12/29", "۱۴۰۲/۱۲/۲۹"},
	} {
		if got := b.post(path+"/choose", url.Values{"answer": {tc.input}, "revision": {strconv.Itoa(revision)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		revision++
		page := b.send("GET", path, nil).Body.String()
		if !strings.Contains(page, "تاریخ:\n"+tc.display) || !strings.Contains(page, `value="submit"`) {
			t.Fatalf("valid date %q missing canonical Jalali display %q", tc.input, tc.display)
		}
		if got := b.post(path+"/choose", url.Values{"choice": {"back"}, "revision": {strconv.Itoa(revision)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		revision++
	}
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview recorded a real Submission")
	}
}

func TestBookingConfigurationSavesCustomQuestionsAndRequestAcknowledgement(t *testing.T) {
	_, b := draftFixture(t)
	page := b.send("GET", "/bots/1/draft?template=booking", nil)
	for _, want := range []string{"قالب درخواست رزرو", "تاریخ شمسی", "رزرو قطعی نیست"} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("Booking defaults missing %q: %d", want, page.Code)
		}
	}
	if got := b.postDraft(t, "/bots/1/draft", bookingDraft()); got.Code != 303 {
		t.Fatalf("save Booking: %d", got.Code)
	}
	page = b.send("GET", "/bots/1/draft", nil)
	for _, want := range []string{`value="booking"`, "تاریخ ترجیحی", "تاریخ ترجیحی شما چیست؟", "رزرو قطعی نیست"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("saved Booking settings missing %q", want)
		}
	}
	bad := bookingDraft()
	bad["question_required"][1] = "maybe"
	if got := b.postDraft(t, "/bots/1/draft", bad); got.Code != 422 {
		t.Fatal("invalid required setting accepted")
	}
	if got := b.send("POST", "/bots/1/draft", bookingDraft()); got.Code != 403 {
		t.Fatal("Booking save bypassed CSRF")
	}
}

func TestBookingPreviewReviewsEditsSkipsAndConfirmsWithoutRealSubmission(t *testing.T) {
	_, b := draftFixture(t)
	v := bookingDraft()
	v["question_required"][1] = "no"
	if got := b.postDraft(t, "/bots/1/draft", v); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(values url.Values, want string) {
		t.Helper()
		values.Set("revision", strconv.Itoa(revision))
		if got := b.post(path+"/choose", values); got.Code != 303 {
			t.Fatalf("step %d: %d", revision, got.Code)
		}
		revision++
		if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), want) {
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
	if got := b.post(path+"/choose", url.Values{"revision": {"6"}, "answer": {"1404/1/1"}}); got.Code != 409 {
		t.Fatalf("stale date step accepted: %d", got.Code)
	}
	advance(url.Values{"choice": {"submit"}}, "رزرو قطعی نیست")
	if got := b.post(path+"/choose", url.Values{"revision": {strconv.Itoa(revision)}, "choice": {"submit"}}); got.Code != 422 {
		t.Fatal("repeated Preview confirmation accepted")
	}
	advance(url.Values{"choice": {"again"}}, "انتخاب کنید")
	advance(url.Values{"choice": {"booking"}}, "نام شما چیست؟")
	advance(url.Values{"answer": {"مینا"}}, "تاریخ ترجیحی شما چیست؟")
	advance(url.Values{"choice": {"cancel"}}, "درخواست لغو شد")
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created a real Submission")
	}
}
