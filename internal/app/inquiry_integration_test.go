package app

import (
	"fmt"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func inquiryDraft() url.Values {
	v := url.Values{"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"}, "form_label": {"درخواست"}, "review_message": {"پاسخ\u200cها را بررسی کنید"}, "acknowledgement": {"درخواست شما دریافت شد"}, "question_label": {"نام", "تماس", "درخواست"}, "question_prompt": {"نام شما چیست؟", "شماره تماس شما چیست؟", "درخواست شما چیست؟"}, "question_required": {"yes", "yes", "no"}}
	v["form_id"] = []string{"inquiry"}
	v["form_choice_id"] = []string{"inquiry"}
	v["question_form"] = []string{"inquiry", "inquiry", "inquiry"}
	v["question_id"] = []string{"name", "contact", "request"}
	v["question_type"] = []string{"short_text", "phone", "long_text"}
	v["question_options"] = []string{"", "", ""}
	v["number_min"] = []string{"", "", ""}
	v["number_max"] = []string{"", "", ""}
	v["text_max"] = []string{"", "", ""}
	v["date_min"] = []string{"", "", ""}
	v["date_max"] = []string{"", "", ""}
	return v
}

type formDriver struct {
	t            *testing.T
	a            *App
	b            *accountBrowser
	f            *telegramFake
	secret       string
	update, sent int
}

func newInquiryDriver(t *testing.T) *formDriver {
	t.Helper()
	return newFormDriver(t, inquiryDraft())
}

func newFormDriver(t *testing.T, draft url.Values) *formDriver {
	t.Helper()
	a, b, f := deliveryFixture(t)
	for _, p := range []string{"draft", "publish", "activate"} {
		values := url.Values{}
		if p == "draft" {
			values = draft
		}
		if p == "activate" {
			values.Set("operate", "yes")
		}
		if got := b.post("/bots/1/"+p, values); got.Code != 303 {
			t.Fatalf("%s: %d", p, got.Code)
		}
	}
	f.mu.Lock()
	secret := f.secret
	f.mu.Unlock()
	return &formDriver{t: t, a: a, b: b, f: f, secret: secret, update: 100}
}

func (d *formDriver) text(value string, count int) {
	d.t.Helper()
	d.update++
	payload := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":%q}}`, d.update, value)
	if got := webhook(d.a, d.secret, payload); got.Code != 200 {
		d.t.Fatal(got.Code)
	}
	d.sent += count
	waitSent(d.t, d.f, d.sent)
}

func (d *formDriver) button(label string) string {
	d.t.Helper()
	sent := waitSent(d.t, d.f, d.sent)
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
	if got := webhook(d.a, d.secret, callbackPayload(d.update, strconv.Itoa(d.update), 77, data)); got.Code != 200 {
		d.t.Fatal(got.Code)
	}
	d.sent += count
	waitSent(d.t, d.f, d.sent)
}

func (d *formDriver) countSubmissions(want int) {
	d.t.Helper()
	page := d.b.send("GET", "/bots/1/submissions", nil)
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
	updated := inquiryDraft()
	updated["question_prompt"][1] = "پرسش تازه"
	updated.Set("acknowledgement", "دریافت نسخه تازه")
	if got := d.b.post("/bots/1/draft", updated); got.Code != 303 {
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
	d.a = restarted
	d.b.router = restarted.server.Handler
	runDeliveryApp(t, restarted)
	d.text("/start", 1)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Continue changed version or lost progress")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	if page := d.b.send("GET", "/bots/1/submissions", nil); !strings.Contains(page.Body.String(), "نام قدیم") {
		t.Fatal("Submission list omits collected answer")
	}
	sent = waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "درخواست شما دریافت شد" {
		t.Fatal("existing attempt switched acknowledgement version")
	}
	d.countSubmissions(1)
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("نام تازه", 1)
	sent = waitSent(t, d.f, d.sent)
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
	webhook(d.a, d.secret, callbackPayload(d.update, "cancelled", 77, old))
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
	webhook(d.a, d.secret, callbackPayload(d.update, "expired", 77, old))
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
	if got := webhook(d.a, d.secret, callbackPayload(d.update, "confirm", 77, button)); got.Code != 200 {
		t.Fatal(got.Code)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if page := d.b.send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), "تحویل پیام با خطا") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.countSubmissions(0)
	if _, err := d.a.db.Exec("DROP TRIGGER reject_submission"); err != nil {
		t.Fatal(err)
	}
	d.f.mu.Lock()
	d.f.sendFailures = 1
	d.f.mu.Unlock()
	// Accelerate the persisted worker backoff; assertions remain on HTTP results.
	if _, err := d.a.db.Exec("UPDATE bot_delivery SET retry_at=0"); err != nil {
		t.Fatal(err)
	}
	d.sent++
	waitSent(t, d.f, d.sent)
	d.countSubmissions(1)
	page := d.b.send("GET", "/bots/1/submissions/1", nil)
	if !strings.Contains(page.Body.String(), "&lt;script&gt;private answer&lt;/script&gt;") || strings.Contains(page.Body.String(), "<script>private answer") {
		t.Fatal("answer not escaped")
	}
	d.update++
	webhook(d.a, d.secret, callbackPayload(d.update, "again-confirm", 77, button))
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
	other := newAccountBrowser(t, d.a.server.Handler)
	for _, p := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.send("GET", p, nil); got.Code != 303 {
			t.Fatal("anonymous records accessible")
		}
	}
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("submission-other@example.test", "Other", "OwnerPassword123"))
	for _, p := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.send("GET", p, nil); got.Code != 404 || strings.Contains(got.Body.String(), "مینا") {
			t.Fatal("cross-owner answers accessible")
		}
	}
	if got := d.b.send("GET", "/account", nil); got.Code != 200 {
		t.Fatal("migration lost owner session")
	}
	if got := d.b.send("GET", "/bots/1/submissions/99", nil); got.Code != 404 {
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
	if got := webhook(d.a, d.secret, payload); got.Code != 200 {
		t.Fatal(got.Code)
	}
	d.sent += 2
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-2].Text != "فقط پاسخ متنی بفرستید یا از دکمه\u200cهای همین پرسش استفاده کنید." || sent[len(sent)-1].Text != "درخواست شما چیست؟" {
		t.Fatal("unsupported input changed progress")
	}
	d.countSubmissions(0)
}

func TestInquiryLongAnswersFitTelegramAndRemainCompleteInSubmission(t *testing.T) {
	d := newInquiryDriver(t)
	values := inquiryDraft()
	values["question_label"][2] = strings.Repeat("😀", 80)
	if got := d.b.post("/bots/1/draft", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("مینا", 1)
	d.text("09123456789", 1)
	answer := strings.Repeat("😀", 2000)
	d.text(answer, 5)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-2].Text+sent[len(sent)-1].Text != strings.Repeat("😀", 80)+":\n"+answer {
		t.Fatal("summary truncated a valid answer")
	}
	for _, m := range sent {
		if len(utf16.Encode([]rune(m.Text))) > 4096 {
			t.Fatal("Telegram text exceeds API bound")
		}
	}
	d.press("ارسال", 1)
	if page := d.b.send("GET", "/bots/1/submissions/1", nil); !strings.Contains(page.Body.String(), answer) {
		t.Fatal("saved long answer truncated")
	}
}

func TestInquiryPreviewQuestionValidationAndRequiredSkip(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", inquiryDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(values url.Values, status int, want string) {
		t.Helper()
		values.Set("revision", strconv.Itoa(revision))
		if got := b.post(path+"/choose", values); got.Code != status {
			t.Fatalf("action status: %d", got.Code)
		}
		if status == 303 {
			revision++
		}
		if want != "" {
			if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), want) {
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
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("cancelled Preview persisted")
	}
}

func TestInquiryConfigurationValidationPreservesDraftAndRequiresCSRF(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.send("GET", "/bots/1/draft?template=inquiry", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "قالب درخواست") {
		t.Fatal("default Inquiry configuration unavailable")
	}
	if got := b.post("/bots/1/draft", inquiryDraft()); got.Code != 303 {
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
			v := inquiryDraft()
			tc.change(v)
			if got := b.post("/bots/1/draft", v); got.Code != 422 {
				t.Fatal(got.Code)
			}
		})
	}
	if got := b.send("GET", "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "نام شما چیست؟") {
		t.Fatal("invalid settings changed saved Draft")
	}
	if got := b.send("POST", "/bots/1/draft", inquiryDraft()); got.Code != 403 {
		t.Fatal("Inquiry save bypassed CSRF")
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("inquiry-other@example.test", "Other", "OwnerPassword123"))
	if got := other.post("/bots/1/draft", inquiryDraft()); got.Code != 404 {
		t.Fatal("Inquiry save bypassed ownership")
	}
}

func TestInquiryConfirmationCreatesOneSubmissionPerAttempt(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.post("/bots/1/draft", inquiryDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.mu.Unlock()
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
		if page := b.send("GET", "/bots/1/submissions", nil); strings.Count(page.Body.String(), "data-submission-id=") != attempt {
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
		if page := b.send("GET", "/bots/1/submissions", nil); page.Code != 200 || strings.Count(page.Body.String(), "data-submission-id=") != attempt+1 {
			t.Fatalf("Submission count: %s", page.Body.String())
		}
		page := b.send("GET", fmt.Sprintf("/bots/1/submissions/%d", attempt+1), nil)
		for _, want := range []string{"مینا", "09123456789", fmt.Sprintf("سفارش %d", attempt+1)} {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("missing collected answer %q", want)
			}
		}
	}
	stop()
}

func TestInquiryPreviewCollectsValidatesEditsAndConfirmsWithoutRealSubmissions(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", inquiryDraft()); got.Code != 303 {
		t.Fatalf("save Inquiry: %d", got.Code)
	}
	path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	revision := 1
	advance := func(values url.Values, expected string) {
		t.Helper()
		values.Set("revision", strconv.Itoa(revision))
		if got := b.post(path+"/choose", values); got.Code != 303 {
			t.Fatalf("Preview action: %d", got.Code)
		}
		revision++
		if page := b.send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), expected) {
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
	if page := b.send("GET", "/bots/1/submissions", nil); page.Code != 200 || strings.Contains(page.Body.String(), "سارا") {
		t.Fatalf("Preview created a real Submission: %d", page.Code)
	}
}
