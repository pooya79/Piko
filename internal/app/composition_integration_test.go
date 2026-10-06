package app

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestCombinedFormsTelegramCompletesEachRouteAndPinsReorderedQuestionsAcrossPublication(t *testing.T) {
	d := newFormDriver(t, fixture.CombinedDraft())
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("مینا", 1)
	changed := d.b.LoadDraft(t, 1)
	// Reorder Inquiry questions in a later version while a Participant answers
	// the original published version.
	for _, key := range []string{"question_form", "question_id", "question_type", "question_label", "question_prompt", "question_required", "question_options", "number_min", "number_max", "text_max", "date_min", "date_max"} {
		changed[key][2], changed[key][3] = changed[key][3], changed[key][2]
	}
	changed["question_prompt"][1] = "نام تازه؟"
	changed["question_label"][1] = "نام تازه"
	if err := d.b.SaveDraft(t, 1, changed); err != nil {
		t.Fatal(err)
	}
	if got := d.b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.Router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "تماس؟" {
		t.Fatal("in-progress Form changed version/order")
	}
	d.text("۰۹۱۲۳۴۵۶۷۸۹", 1)
	d.text("درخواست قدیمی", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	d.press("شروع دوباره", 2)
	d.press("ثبت\u200cنام", 1)
	d.press("علوم", 1)
	d.text("۳٫۵", 3)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if sent := waitSent(t, d.f, d.Sent); !strings.Contains(sent[len(sent)-1].Text, "پذیرش تضمین نمی\u200cشود") {
		t.Fatal("Registration receipt semantics")
	}
	d.press("شروع دوباره", 2)
	d.press("رزرو", 1)
	d.text("۱۴۰۵/۷/۱۱", 2)
	d.press("ارسال", 1)
	d.countSubmissions(3)
	if sent := waitSent(t, d.f, d.Sent); !strings.Contains(sent[len(sent)-1].Text, "رزرو قطعی نیست") {
		t.Fatal("Booking receipt semantics")
	}
	for _, tc := range []struct {
		id     int
		values []string
	}{{1, []string{"مینا", "09123456789", "درخواست قدیمی"}}, {2, []string{"علوم", "3.5"}}, {3, []string{"۱۴۰۵/۰۷/۱۱"}}} {
		page := d.b.Send("GET", "/bots/1/submissions/"+strconv.Itoa(tc.id), nil)
		for _, want := range tc.values {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("Submission %d missing %s", tc.id, want)
			}
		}
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("سارا", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "درخواست؟" {
		t.Fatal("new Interaction did not follow reordered questions")
	}
}

func TestCombinedFormsDraftAndPreviewStayIsolatedFromLive(t *testing.T) {
	d := newFormDriver(t, fixture.CombinedDraft())
	runDeliveryApp(t, d.a)
	old := d.b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	changed := fixture.CombinedDraft()
	changed["question_prompt"][0] = "نام پیش\u200cنویس تازه؟"
	if err := d.b.SaveDraft(t, 1, changed); err != nil {
		t.Fatal(err)
	}
	fresh := d.b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
	for _, tc := range []struct{ path, prompt string }{{old, "نام؟"}, {fresh, "نام پیش\u200cنویس تازه؟"}} {
		if got := d.b.Post(tc.path+"/choose", url.Values{"choice": {"ask"}, "revision": {"1"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := d.b.Send("GET", tc.path, nil)
		if !strings.Contains(page.Body.String(), tc.prompt) {
			t.Fatal("Preview snapshot lost", tc.prompt)
		}
	}
	d.text("/start", 2)
	d.press("درخواست", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "نام؟" {
		t.Fatal("Draft changed live Form before publication")
	}
	d.countSubmissions(0)
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("combined-preview-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{old, fresh} {
		if got := other.Send("GET", path, nil); got.Code != 404 {
			t.Fatal("Preview owner isolation")
		}
		if got := other.Post(path+"/choose", url.Values{"answer": {"داده دیگر"}, "revision": {"2"}}); got.Code != 404 {
			t.Fatal("Preview mutation owner isolation")
		}
	}
}
