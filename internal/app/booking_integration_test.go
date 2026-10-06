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

func TestBookingTelegramEditsDatesAndStoresOneRequestPerAttempt(t *testing.T) {
	d := newFormDriver(t, fixture.BookingDraft())
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست رزرو", 1)
	d.text("<b>مینا</b>", 1)
	d.text("۱۴۰۴/۱۲/۳۰", 2)
	sent := waitSent(t, d.f, d.Sent)
	if !strings.Contains(sent[len(sent)-2].Text, "تاریخ شمسی معتبر") {
		t.Fatal("invalid Telegram date omitted feedback")
	}
	d.text("۱۴۰۳/۱۲/۳۰", 1)
	d.text("اتاق برای دو نفر", 4)
	d.countSubmissions(0)
	d.press("ویرایش پاسخ\u200cها", 1)
	d.press("تاریخ ترجیحی", 1)
	d.text("١٤٠٤/١/١", 4)
	sent = waitSent(t, d.f, d.Sent)
	if sent[len(sent)-2].Text != "تاریخ ترجیحی:\n۱۴۰۴/۰۱/۰۱" {
		t.Fatal("Telegram review lost edited Jalali date")
	}
	confirm := d.button("ارسال")
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "foreign-confirm", 88, confirm))
	waitAnswers(t, d.f, 4)
	d.countSubmissions(0)
	d.press("ارسال", 1)
	if sent := waitSent(t, d.f, d.Sent); !strings.Contains(sent[len(sent)-1].Text, "رزرو قطعی نیست") {
		t.Fatal("Booking acknowledgement omitted request semantics")
	}
	webhook(d.a, d.Secret, callbackPayload(d.update, strconv.Itoa(d.update), 77, confirm))
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "repeat-confirm", 77, confirm))
	waitAnswers(t, d.f, 6)
	d.countSubmissions(1)
	page := d.b.Send("GET", "/bots/1/submissions/1", nil)
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
	if page := d.b.Send("GET", "/bots/1/submissions/2", nil); !strings.Contains(page.Body.String(), "۱۴۰۵/۰۷/۱۱") || !strings.Contains(page.Body.String(), "بدون پاسخ") {
		t.Fatal("second request lost normalized date or skipped details")
	}
	other := fixture.NewAccountBrowser(t, d.a.server.Handler, d.a.cfg.DatabasePath)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("booking-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/preview", "/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.Send("GET", path, nil); got.Code != 404 {
			t.Fatalf("other owner accessed %s: %d", path, got.Code)
		}
	}
	if err := other.SaveDraft(t, 1, fixture.BookingDraft()); err == nil {
		t.Fatal("rejected Draft change was accepted")
	}
}

func TestBookingRetainsDateQuestionAndAnswersAcrossPublicationAndRestart(t *testing.T) {
	d := newFormDriver(t, fixture.BookingDraft())
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست رزرو", 1)
	d.text("مینا", 1)
	changed := fixture.BookingDraft()
	changed["question_prompt"][1] = "تاریخ تازه؟"
	changed["question_label"][1] = "تاریخ تازه"
	changed.Set("acknowledgement", "درخواست تازه دریافت شد")
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
	sent := waitSent(t, d.f, d.Sent)
	if !strings.Contains(sent[len(sent)-1].Text, "تاریخ ترجیحی شما چیست؟") || strings.Contains(sent[len(sent)-1].Text, "تاریخ تازه؟") {
		t.Fatal("unfinished Booking switched Flow version")
	}
	d.text("۱۴۰۳/۱۲/۳۰", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	page := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String()
	if !strings.Contains(page, "تاریخ ترجیحی") || !strings.Contains(page, "۱۴۰۳/۱۲/۳۰") || strings.Contains(page, "تاریخ تازه") {
		t.Fatal("stored date or frozen label lost across restart")
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست رزرو", 1)
	d.text("سارا", 1)
	sent = waitSent(t, d.f, d.Sent)
	if !strings.Contains(sent[len(sent)-1].Text, "تاریخ تازه؟") {
		t.Fatal("new Booking did not select latest published version")
	}
}
