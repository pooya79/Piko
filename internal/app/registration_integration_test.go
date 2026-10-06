package app

import (
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestRegistrationTelegramReviewsEditsAndStoresExactAnswersOncePerAttempt(t *testing.T) {
	d := newFormDriver(t, fixture.RegistrationDraft())
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست ثبت\u200cنام", 1)
	d.text("<b>مینا</b>", 1)
	oldChoice := d.button("هنر")
	d.text("انتخاب نادرست", 2)
	d.press("هنر", 1)
	// A button from the choice question cannot answer the numeric step.
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "old-choice", 77, oldChoice))
	waitAnswers(t, d.f, 3)
	d.text("1e1000000", 2)
	d.text("۹۰۰۷۱۹۹۲۵۴۷۴۰۹۹۳٫۱۲۳۴۵۶۷۸۹۰۱۲۳۴۵۶۷۸۹", 4)
	d.countSubmissions(0)
	d.press("ویرایش پاسخ\u200cها", 1)
	d.press("دوره", 1)
	d.press("علوم", 4)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "مقدار:\n9007199254740993.1234567890123456789" || sent[len(sent)-2].Text != "دوره:\nعلوم" {
		t.Fatal("summary lost edited choice or exact numeric answer")
	}
	confirm := d.button("ارسال")
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "other-participant", 88, confirm))
	waitAnswers(t, d.f, 7)
	d.countSubmissions(0)
	d.press("ارسال", 1)
	if sent := waitSent(t, d.f, d.Sent); !strings.Contains(sent[len(sent)-1].Text, "پذیرش یا ظرفیت تضمین نمی\u200cشود") {
		t.Fatal("Registration acknowledgement guarantees acceptance")
	}
	webhook(d.a, d.Secret, callbackPayload(d.update, strconv.Itoa(d.update), 77, confirm))
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "repeat-confirm", 77, confirm))
	waitAnswers(t, d.f, 9)
	d.countSubmissions(1)
	page := d.b.Send("GET", "/bots/1/submissions/1", nil)
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
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("registration-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/draft", "/bots/1/preview", "/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.Send("GET", path, nil); got.Code != 404 {
			t.Fatalf("other owner accessed %s: %d", path, got.Code)
		}
	}
	if got := other.PostDraft(t, "/bots/1/draft", fixture.RegistrationDraft()); got.Code != 404 {
		t.Fatal("cross-owner Registration save")
	}
}
