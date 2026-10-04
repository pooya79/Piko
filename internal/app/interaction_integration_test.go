package app

import (
	"github.com/pooya79/Piko/internal/bot/telegram"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInteractionStartAgainAbandonsAnswersAndSelectsNewestPublication(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پاسخ رهاشده", 1)
	oldBack := d.button("بازگشت")
	updated := inquiryDraft()
	updated.Set("welcome", "سلام نسخه تازه")
	updated["question_prompt"][0] = "نام تازه؟"
	if got := d.b.post("/bots/1/draft", updated); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	oldContinue := d.button("ادامه")
	d.press("شروع دوباره", 2)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-2].Text != "سلام نسخه تازه" {
		t.Fatal("reset ignored latest publication")
	}
	d.obsolete(oldContinue)
	d.obsolete(oldBack)
	d.press("درخواست", 1)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "نام تازه؟" {
		t.Fatal("reset retained original Flow")
	}
	d.text("نام تازه", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.countSubmissions(0)
	d.press("ارسال", 1)
	page := d.b.send("GET", "/bots/1/submissions/1", nil)
	if !strings.Contains(page.Body.String(), "نام تازه") || strings.Contains(page.Body.String(), "پاسخ رهاشده") {
		t.Fatal("reset answers leaked into confirmed Submission")
	}
}

func TestInteractionResumePreservesReviewEditingAndCancellation(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام اولیه", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	oldSubmit := d.button("ارسال")
	d.text("/start", 1)
	d.obsolete(oldSubmit)
	d.press("ادامه", 4)
	d.press("ویرایش پاسخ\u200cها", 1)
	d.text("/start", 1)
	d.press("ادامه", 1)
	d.press("نام", 1)
	d.text("/start", 1)
	d.press("ادامه", 1)
	d.press("بازگشت", 4)
	d.press("بازگشت", 1)
	d.text("پاسخ اصلاح\u200cشده", 4)
	d.text("/start", 1)
	d.press("ادامه", 4)
	oldSubmit = d.button("ارسال")
	d.press("لغو", 1)
	d.obsolete(oldSubmit)
	d.countSubmissions(0)
	d.text("/start", 2)
	d.press("درخواست", 1)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "نام شما چیست؟" {
		t.Fatal("cancelled progress resumed")
	}
}

func TestInteractionExpiryBoundaryAndCleanupRetainSubmissions(t *testing.T) {
	for _, mode := range []string{"start", "answer", "callback", "cleanup", "restart"} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			base := time.Now().Unix()
			clock.Store(base)
			now := func() time.Time { return time.Unix(clock.Load(), 0) }
			a, b, f := deliveryFixtureClock(t, now)
			d := configureFormDriver(t, a, b, f, inquiryDraft())
			stop := runDeliveryApp(t, d.a)
			d.text("/start", 2)
			d.press("درخواست", 1)
			d.text("نام ثبت\u200cشده", 1)
			d.text("09123456789", 1)
			d.press("رد کردن", 4)
			d.press("ارسال", 1)
			d.press("شروع دوباره", 2)
			d.press("درخواست", 1)
			d.text("نام ناتمام", 1)
			d.text("09123456789", 1)
			d.press("رد کردن", 4)
			oldSubmit := d.button("ارسال")
			clock.Store(base + 86400 - 1)
			if err := d.a.cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			d.text("/start", 1)
			d.press("ادامه", 4)
			if sent := waitSent(t, d.f, d.sent); !strings.Contains(sent[len(sent)-3].Text, "نام ناتمام") {
				t.Fatal("cleanup removed unexpired progress")
			}
			clock.Store(base + 86400 + 99)
			d.obsolete(oldSubmit)
			oldSubmit = d.button("ارسال")
			// Continue renews inactivity; the exact renewed deadline is expired.
			clock.Store(base + 2*86400 - 1)
			switch mode {
			case "start":
				d.text("/start", 3)
			case "answer":
				d.text("پاسخ دیرهنگام", 3)
			case "callback":
				d.obsolete(oldSubmit)
			case "cleanup":
				if err := d.a.cleanup(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "restart":
				stop()
				restarted, err := newWithTelegramClock(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient), now)
				if err != nil {
					t.Fatal(err)
				}
				d.a, d.b.router = restarted, restarted.server.Handler
				runDeliveryApp(t, restarted)
				// Wait for startup cleanup before rewinding the test clock.
				d.text("/start", 2)
			}
			// Rewinding cannot resurrect deleted answers or revive old Submit.
			clock.Store(base)
			if mode == "callback" || mode == "cleanup" {
				d.text("/start", 2)
			}
			d.obsolete(oldSubmit)
			d.press("درخواست", 1)
			if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "نام شما چیست؟" {
				t.Fatal("expired progress was retained")
			}
			d.countSubmissions(1)
			if page := d.b.send("GET", "/bots/1/submissions/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "نام ثبت\u200cشده") {
				t.Fatal("expiry removed completed Submission or its Flow definition")
			}
		})
	}
}

func (d *formDriver) obsolete(data string) {
	d.t.Helper()
	d.f.mu.Lock()
	acknowledged := len(d.f.answers)
	d.f.mu.Unlock()
	d.update++
	if got := webhook(d.a, d.secret, callbackPayload(d.update, "obsolete", 77, data)); got.Code != 200 {
		d.t.Fatal(got.Code)
	}
	waitAnswers(d.t, d.f, acknowledged+1)
}

func TestInteractionExpiredAccessClearsProgressAndExplainsFreshStart(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام منقضی", 1)
	if _, err := d.a.db.Exec("UPDATE bot_participants SET expires_at=unixepoch()"); err != nil {
		t.Fatal(err)
	}
	d.text("/start", 3)
	sent := waitSent(t, d.f, d.sent)
	if !strings.Contains(sent[len(sent)-3].Text, "منقضی") {
		t.Fatal("expired Start did not explain discarded progress")
	}
	d.press("درخواست", 1)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "نام شما چیست؟" {
		t.Fatal("expired answers were resumed")
	}
	d.countSubmissions(0)
}

func TestCompletedInteractionExpiryStartsFreshWithoutClaimingSubmissionWasAbandoned(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام ثبت\u200cشده", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	if _, err := d.a.db.Exec("UPDATE bot_participants SET expires_at=unixepoch()"); err != nil {
		t.Fatal(err)
	}
	before := d.sent
	d.text("/start", 2)
	sent := waitSent(t, d.f, d.sent)
	if sent[before].Text != "سلام" || sent[before+1].Text != "انتخاب کنید" {
		t.Fatal("completed Interaction was described as abandoned unfinished answers")
	}
	d.countSubmissions(1)
}

func TestInteractionStartingFromOlderMenuRefreshesLatestPublicationBeforeCollectingAnswers(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	oldMenu := d.button("درخواست")
	updated := inquiryDraft()
	updated.Set("welcome", "نسخه تازه منو")
	updated.Set("form_choice_id", "updated-inquiry")
	updated.Set("form_label", "درخواست تازه")
	updated["question_prompt"][0] = "نام در نسخه تازه؟"
	if got := d.b.post("/bots/1/draft", updated); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.press("درخواست", 2)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-2].Text != "نسخه تازه منو" {
		t.Fatal("new Interaction would start from a superseded publication")
	}
	d.obsolete(oldMenu)
	d.press("درخواست تازه", 1)
	sent = waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "نام در نسخه تازه؟" {
		t.Fatal("fresh menu did not start newest Form")
	}
	d.countSubmissions(0)
}
