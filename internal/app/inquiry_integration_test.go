package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

type formDriver struct {
	t            *testing.T
	a            *App
	b            *fixture.Browser
	f            *fixture.TelegramFake
	Secret       string
	update, Sent int
}

func newInquiryDriver(t *testing.T) *formDriver {
	t.Helper()
	return newFormDriver(t, fixture.InquiryDraft())
}

func newFormDriver(t *testing.T, draft url.Values) *formDriver {
	t.Helper()
	a, b, f := deliveryFixture(t)
	return configureFormDriver(t, a, b, f, draft)
}

func configureFormDriver(t *testing.T, a *App, b *fixture.Browser, f *fixture.TelegramFake, draft url.Values) *formDriver {
	t.Helper()
	if got := b.PostDraft(t, "/bots/1/draft", draft); got.Code != 303 {
		t.Fatalf("draft: %d", got.Code)
	}
	for _, p := range []string{"publish", "activate"} {
		values := url.Values{}
		if p == "activate" {
			values.Set("operate", "yes")
		}
		if got := b.Post("/bots/1/"+p, values); got.Code != 303 {
			t.Fatalf("%s: %d", p, got.Code)
		}
	}
	f.Mu.Lock()
	secret := f.Secret
	f.Mu.Unlock()
	return &formDriver{t: t, a: a, b: b, f: f, Secret: secret, update: 100}
}

func (d *formDriver) text(value string, count int) {
	d.t.Helper()
	d.update++
	payload := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":%q}}`, d.update, value)
	if got := webhook(d.a, d.Secret, payload); got.Code != 200 {
		d.t.Fatal(got.Code)
	}
	d.Sent += count
	waitSent(d.t, d.f, d.Sent)
}

func (d *formDriver) button(label string) string {
	d.t.Helper()
	sent := waitSent(d.t, d.f, d.Sent)
	m := sent[len(sent)-1]
	if m.Markup != nil {
		for _, row := range m.Markup.Buttons {
			for _, b := range row {
				if b.Text == label {
					return b.Data
				}
			}
		}
	}
	d.t.Fatalf("missing %q", label)
	return ""
}

func (d *formDriver) press(label string, count int) {
	d.t.Helper()
	data := d.button(label)
	d.update++
	if got := webhook(d.a, d.Secret, callbackPayload(d.update, strconv.Itoa(d.update), 77, data)); got.Code != 200 {
		d.t.Fatal(got.Code)
	}
	d.Sent += count
	waitSent(d.t, d.f, d.Sent)
}

func (d *formDriver) countSubmissions(want int) {
	d.t.Helper()
	page := d.b.Send("GET", "/bots/1/submissions", nil)
	if page.Code != 200 || strings.Count(page.Body.String(), "data-submission-id=") != want {
		d.t.Fatalf("expected %d Submissions: status %d", want, page.Code)
	}
}

func TestInquiryRestartContinueAndVersionContinuity(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام قدیم", 1)
	updated := fixture.InquiryDraft()
	updated["question_prompt"][1] = "پرسش تازه"
	updated.Set("acknowledgement", "دریافت نسخه تازه")
	if got := d.b.PostDraft(t, "/bots/1/draft", updated); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a = restarted
	d.b.Router = restarted.server.Handler
	stop = runDeliveryApp(t, restarted)
	d.text("/start", 1)
	d.button("ادامه")
	d.button("شروع دوباره")
	stop()
	restarted, err = newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.Router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	d.text("پاسخ پیش از انتخاب ادامه", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Continue changed version or lost progress")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	if page := d.b.Send("GET", "/bots/1/submissions", nil); !strings.Contains(page.Body.String(), "نام قدیم") {
		t.Fatal("Submission list omits collected answer")
	}
	sent = waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "درخواست شما دریافت شد" {
		t.Fatal("existing attempt switched acknowledgement version")
	}
	d.countSubmissions(1)
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("نام تازه", 1)
	sent = waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "پرسش تازه" {
		t.Fatal("new attempt did not select latest version")
	}
}

func TestInquiryBackCancelAndExpiredButtonsCannotSubmit(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام اول", 1)
	d.press("بازگشت", 1)
	d.text("نام اصلاح\u200cشده", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	old := d.button("ارسال")
	d.press("لغو", 1)
	d.countSubmissions(0)
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "cancelled", 77, old))
	waitAnswers(t, d.f, 5)
	d.countSubmissions(0)
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("نام دوم", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	old = d.button("ارسال")
	if _, err := d.a.db.Exec("UPDATE bot_participants SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "expired", 77, old))
	waitAnswers(t, d.f, 9)
	d.countSubmissions(0)
	d.text("/start", 2)
	if err := d.a.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestInquiryConfirmationRollbackAndOutboundFailureRemainDuplicateSafe(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("<script>private answer</script>", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	if _, err := d.a.db.Exec(`CREATE TRIGGER reject_submission BEFORE UPDATE ON bot_updates WHEN NEW.output IS NOT NULL BEGIN SELECT RAISE(ABORT,'private answer'); END`); err != nil {
		t.Fatal(err)
	}
	button := d.button("ارسال")
	d.update++
	if got := webhook(d.a, d.Secret, callbackPayload(d.update, "confirm", 77, button)); got.Code != 200 {
		t.Fatal(got.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if page := d.b.Send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), "تحویل پیام با خطا") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.countSubmissions(0)
	if _, err := d.a.db.Exec("DROP TRIGGER reject_submission"); err != nil {
		t.Fatal(err)
	}
	d.f.Mu.Lock()
	d.f.SendFailures = 1
	d.f.Mu.Unlock()
	// Accelerate the persisted worker backoff; assertions remain on HTTP results.
	if _, err := d.a.db.Exec("UPDATE bot_delivery SET retry_at=0"); err != nil {
		t.Fatal(err)
	}
	d.Sent++
	waitSent(t, d.f, d.Sent)
	d.countSubmissions(1)
	page := d.b.Send("GET", "/bots/1/submissions/1", nil)
	if !strings.Contains(page.Body.String(), "&lt;script&gt;private answer&lt;/script&gt;") || strings.Contains(page.Body.String(), "<script>private answer") {
		t.Fatal("answer not escaped")
	}
	d.update++
	webhook(d.a, d.Secret, callbackPayload(d.update, "again-confirm", 77, button))
	waitAnswers(t, d.f, 4)
	d.countSubmissions(1)
}

func TestInquirySubmissionPagesRequireOwnerAndSurviveForwardMigration(t *testing.T) {
	d := newInquiryDriver(t)
	if err := database.Migrate(t.Context(), d.a.db, true); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), d.a.db, false); err != nil {
		t.Fatal(err)
	}
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("مینا", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	for _, p := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.Send("GET", p, nil); got.Code != 303 {
			t.Fatal("anonymous records accessible")
		}
	}
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("submission-other@example.test", "Other", "OwnerPassword123"))
	for _, p := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.Send("GET", p, nil); got.Code != 404 || strings.Contains(got.Body.String(), "مینا") {
			t.Fatal("cross-owner answers accessible")
		}
	}
	if got := d.b.Send("GET", "/account", nil); got.Code != 200 {
		t.Fatal("migration lost owner session")
	}
	if got := d.b.Send("GET", "/bots/1/submissions/99", nil); got.Code != 404 {
		t.Fatal("missing Submission returned")
	}
}

func TestInquiryUnsupportedMessagesDoNotSkipOptionalQuestions(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("مینا", 1)
	d.text("09123456789", 1)
	d.update++
	payload := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"photo":[{"file_id":"unsupported"}]}}`, d.update)
	if got := webhook(d.a, d.Secret, payload); got.Code != 200 {
		t.Fatal(got.Code)
	}
	d.Sent += 2
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-2].Text != "فقط پاسخ متنی بفرستید یا از دکمه\u200cهای همین پرسش استفاده کنید." || sent[len(sent)-1].Text != "درخواست شما چیست؟" {
		t.Fatal("unsupported input changed progress")
	}
	d.countSubmissions(0)
}

func TestInquiryLongAnswersFitTelegramAndRemainCompleteInSubmission(t *testing.T) {
	d := newInquiryDriver(t)
	values := fixture.InquiryDraft()
	values["question_label"][2] = strings.Repeat("😀", 80)
	if got := d.b.PostDraft(t, "/bots/1/draft", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("مینا", 1)
	d.text("09123456789", 1)
	answer := strings.Repeat("😀", 2000)
	d.text(answer, 5)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-2].Text+sent[len(sent)-1].Text != strings.Repeat("😀", 80)+":\n"+answer {
		t.Fatal("summary truncated a valid answer")
	}
	for _, m := range sent {
		if len(utf16.Encode([]rune(m.Text))) > 4096 {
			t.Fatal("Telegram text exceeds API bound")
		}
	}
	d.press("ارسال", 1)
	if page := d.b.Send("GET", "/bots/1/submissions/1", nil); !strings.Contains(page.Body.String(), answer) {
		t.Fatal("saved long answer truncated")
	}
}

func TestInquiryConfirmationCreatesOneSubmissionPerAttempt(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.Mu.Unlock()
	stop := runDeliveryApp(t, a)
	update := 100
	count := 0
	send := func(text string, n int) {
		t.Helper()
		update++
		payload := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":%q}}`, update, text)
		if got := webhook(a, secret, payload); got.Code != 200 {
			t.Fatal(got.Code)
		}
		count += n
		waitSent(t, f, count)
	}
	press := func(label string, n int) string {
		t.Helper()
		sent := waitSent(t, f, count)
		message := sent[len(sent)-1]
		data := ""
		if message.Markup != nil {
			for _, row := range message.Markup.Buttons {
				for _, button := range row {
					if button.Text == label {
						data = button.Data
					}
				}
			}
		}
		if data == "" {
			t.Fatalf("missing button %s", label)
		}
		update++
		if got := webhook(a, secret, callbackPayload(update, strconv.Itoa(update), 77, data)); got.Code != 200 {
			t.Fatal(got.Code)
		}
		count += n
		if n > 0 {
			waitSent(t, f, count)
		}
		return data
	}
	for attempt := range 2 {
		send("/start", 2)
		press("درخواست", 1)
		send("مینا", 1)
		send("۰۹۱۲۳۴۵۶۷۸۹", 1)
		send(fmt.Sprintf("سفارش %d", attempt+1), 4)
		if page := b.Send("GET", "/bots/1/submissions", nil); strings.Count(page.Body.String(), "data-submission-id=") != attempt {
			t.Fatal("unfinished answers became Submission")
		}
		button := press("ارسال", 1)
		// Replay both the exact update and a distinct update carrying the old button.
		if got := webhook(a, secret, callbackPayload(update, strconv.Itoa(update), 77, button)); got.Code != 200 {
			t.Fatal(got.Code)
		}
		update++
		if got := webhook(a, secret, callbackPayload(update, strconv.Itoa(update), 77, button)); got.Code != 200 {
			t.Fatal(got.Code)
		}
		waitAnswers(t, f, (attempt+1)*3)
		if page := b.Send("GET", "/bots/1/submissions", nil); page.Code != 200 || strings.Count(page.Body.String(), "data-submission-id=") != attempt+1 {
			t.Fatalf("Submission count: %s", page.Body.String())
		}
		page := b.Send("GET", fmt.Sprintf("/bots/1/submissions/%d", attempt+1), nil)
		for _, want := range []string{"مینا", "09123456789", fmt.Sprintf("سفارش %d", attempt+1)} {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("missing collected answer %q", want)
			}
		}
	}
	stop()
}
