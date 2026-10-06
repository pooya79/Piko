package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBotPauseRetainsQuestionsAndConfirmationWithoutSubmissions(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام محفوظ", 1)
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatalf("pause: %d", got.Code)
	}
	d.text("09123456789", 1)
	if sent := waitSent(t, d.f, d.Sent); !strings.Contains(sent[len(sent)-1].Text, "موقتاً") {
		t.Fatal("paused answer did not explain temporary unavailability")
	}
	d.text("/start", 1)
	d.countSubmissions(0)
	if page := d.b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), `action="/bots/1/resume"`) || !strings.Contains(page.Body.String(), `data-bot-state="paused"`) {
		t.Fatal("persisted pause or resume control missing")
	}
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatalf("resume: %d", got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("paused answer advanced the active question")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	submit := d.button("ارسال")
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.obsolete(submit)
	d.obsolete(submit)
	d.countSubmissions(0)
	d.f.Mu.Lock()
	ack := d.f.AnswerTexts[len(d.f.AnswerTexts)-1]
	d.f.Mu.Unlock()
	if !strings.Contains(ack, "موقتاً") {
		t.Fatal("paused confirmation did not explain temporary unavailability")
	}
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	// A Telegram retry of an update processed during pause stays deduplicated.
	if got := webhook(d.a, d.Secret, callbackPayload(d.update, "obsolete", 77, submit)); got.Code != 200 {
		t.Fatal(got.Code)
	}
	// The original review button remains valid; pause never changes the step.
	d.update++
	if got := webhook(d.a, d.Secret, callbackPayload(d.update, "resumed-submit", 77, submit)); got.Code != 200 {
		t.Fatal(got.Code)
	}
	d.Sent++
	waitSent(t, d.f, d.Sent)
	d.obsolete(submit)
	d.countSubmissions(1)
	if page := d.b.Send("GET", "/bots/1/submissions/1", nil); !strings.Contains(page.Body.String(), "نام محفوظ") {
		t.Fatal("pause lost collected answers")
	}
}

func TestBotPauseSurvivesRestartAndKeepsOriginalInteractionVersion(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام نسخه اصلی", 1)
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	updated := fixture.InquiryDraft()
	updated.Set("welcome", "سلام تازه")
	updated.Set("acknowledgement", "دریافت تازه")
	updated["question_prompt"][1] = "تماس تازه؟"
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
	d.a, d.b.Router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	for _, path := range []string{"/bots/1", "/bots", "/dashboard"} {
		if page := d.b.Send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), `data-bot-state="paused"`) {
			t.Fatalf("pause not retained on %s", path)
		}
	}
	d.text("/start", 1)
	if sent := waitSent(t, d.f, d.Sent); !strings.Contains(sent[len(sent)-1].Text, "موقتاً") {
		t.Fatal("restart resumed a paused Bot")
	}
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("resume switched an unfinished Interaction to the new publication")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "درخواست شما دریافت شد" {
		t.Fatal("resume changed the original acknowledgement")
	}
	d.countSubmissions(1)
	d.press("شروع دوباره", 2)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-2].Text != "سلام تازه" {
		t.Fatal("new Interaction after resume ignored the latest publication")
	}
}

func TestBotPausedAttemptsDoNotRenewExpiryAndCompletedRecordsRemain(t *testing.T) {
	for _, mode := range []string{"access", "cleanup", "restart"} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			base := time.Now().Unix()
			clock.Store(base)
			now := func() time.Time { return time.Unix(clock.Load(), 0) }
			a, b, f := deliveryFixtureClock(t, now)
			d := configureFormDriver(t, a, b, f, fixture.InquiryDraft())
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
			submit := d.button("ارسال")
			if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			clock.Store(base + 86400 - 1)
			d.text("/start", 1)
			d.obsolete(submit)
			clock.Store(base + 86400)
			switch mode {
			case "access":
				d.obsolete(submit)
			case "cleanup":
				if err := d.a.cleanup(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "restart":
				stop()
				restarted, err := newWithTelegramClock(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient), now)
				if err != nil {
					t.Fatal(err)
				}
				d.a, d.b.Router = restarted, restarted.server.Handler
				runDeliveryApp(t, restarted)
				d.text("/start", 1)
			}
			// Rewinding proves access/startup/cleanup deleted rather than hid it.
			clock.Store(base)
			if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			d.obsolete(submit)
			d.text("/start", 2)
			d.press("درخواست", 1)
			if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "نام شما چیست؟" {
				t.Fatal("paused activity renewed expired progress")
			}
			d.countSubmissions(1)
			if page := d.b.Send("GET", "/bots/1/submissions/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "نام ثبت\u200cشده") {
				t.Fatal("pause or expiry removed a completed Submission")
			}
		})
	}
}

func TestBotPausedConfirmationQueuedBeforeResumeCannotSubmit(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام محفوظ", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	submit := d.button("ارسال")
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.Router = restarted, restarted.server.Handler
	t.Cleanup(func() { _ = restarted.db.Close() })
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.f.Mu.Lock()
	acks := len(d.f.Answers)
	d.f.Mu.Unlock()
	d.update++
	if got := webhook(d.a, d.Secret, callbackPayload(d.update, "queued-paused-submit", 77, submit)); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	runDeliveryApp(t, d.a)
	waitAnswers(t, d.f, acks+1)
	d.countSubmissions(0)
	d.f.Mu.Lock()
	ack := d.f.AnswerTexts[len(d.f.AnswerTexts)-1]
	d.f.Mu.Unlock()
	if !strings.Contains(ack, "موقتاً") {
		t.Fatal("queued paused attempt was executed after resume")
	}
}

func TestBotPauseControlsRequireOwnerAuthenticationCSRFAndActiveDelivery(t *testing.T) {
	d := newInquiryDriver(t)
	visitor := fixture.NewAccountBrowser(t, d.a.server.Handler)
	visitor.Send("GET", "/register", nil)
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("pause-other@example.test", "دیگر", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/pause", "/bots/1/resume"} {
		if got := visitor.Post(path, url.Values{}); got.Code != 303 {
			t.Fatalf("anonymous %s: %d", path, got.Code)
		}
		if got := other.Post(path, url.Values{}); got.Code != 404 {
			t.Fatalf("other owner %s: %d", path, got.Code)
		}
		for _, token := range []string{"", "invalid"} {
			if got := d.b.Send("POST", path, url.Values{"csrf_token": {token}}); got.Code != 403 {
				t.Fatalf("CSRF %s: %d", path, got.Code)
			}
		}
		for _, method := range []string{"GET", "PUT"} {
			if got := d.b.Send(method, path, url.Values{"csrf_token": {d.b.Cookie("piko_csrf")}}); got.Code != 405 {
				t.Fatalf("%s %s: %d", method, path, got.Code)
			}
		}
	}
	if page := d.b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), `action="/bots/1/pause"`) || strings.Contains(page.Body.String(), `data-bot-state="paused"`) {
		t.Fatal("rejected attempts changed pause state")
	}
	for _, action := range []string{"pause", "pause", "resume", "resume"} {
		if got := d.b.Post("/bots/1/"+action, url.Values{}); got.Code != 303 {
			t.Fatalf("idempotent %s: %d", action, got.Code)
		}
	}
	_, inactive, _ := deliveryFixture(t)
	for _, action := range []string{"pause", "resume"} {
		if got := inactive.Post("/bots/1/"+action, url.Values{}); got.Code != 409 {
			t.Fatalf("inactive %s: %d", action, got.Code)
		}
	}
}

func TestBotPauseLeavesPreviewIndependentFromLiveProgress(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام زنده", 1)
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	started := d.b.Post("/bots/1/preview", url.Values{})
	if started.Code != 303 {
		t.Fatal(started.Code)
	}
	path := started.Header().Get("Location")
	for i, values := range []url.Values{
		{"choice": {"inquiry"}}, {"answer": {"نام آزمایشی"}}, {"answer": {"09123456789"}}, {"choice": {"skip"}}, {"choice": {"submit"}},
	} {
		values.Set("revision", strconv.Itoa(i+1))
		if got := d.b.Post(path+"/choose", values); got.Code != 303 {
			t.Fatalf("Preview step %d: %d", i, got.Code)
		}
	}
	if page := d.b.Send("GET", path, nil); !strings.Contains(page.Body.String(), "درخواست شما دریافت شد") {
		t.Fatal("paused Bot prevented Preview completion")
	}
	d.countSubmissions(0)
	d.text("09123456789", 1)
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Preview changed live progress")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	if page := d.b.Send("GET", "/bots/1/submissions/1", nil); !strings.Contains(page.Body.String(), "نام زنده") || strings.Contains(page.Body.String(), "نام آزمایشی") {
		t.Fatal("Preview answers leaked into live Submission")
	}
}

func TestBotPausedPollingKeepsReceivingAndResumesPublishedFlow(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	if got := b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	f.PollingUpdates = []json.RawMessage{json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`)}
	f.Mu.Unlock()
	runDeliveryApp(t, a)
	if sent := waitSent(t, f, 1); !strings.Contains(sent[0].Text, "موقتاً") {
		t.Fatal("polling ignored pause")
	}
	if got := b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	f.PollingUpdates = append(f.PollingUpdates, json.RawMessage(`{"update_id":101,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`))
	f.Mu.Unlock()
	if sent := waitSent(t, f, 3); sent[1].Text != "Hello" || sent[2].Text != "Choose" {
		t.Fatal("polling resume lost the Published flow")
	}
}

func TestBotPauseMigrationPreservesExistingOwnerAndPublishedData(t *testing.T) {
	d := newInquiryDriver(t)
	fixture.RollbackToMigration(t, d.a.db, "000008_inquiry_submissions")
	if err := database.Migrate(t.Context(), d.a.db, false); err != nil {
		t.Fatal(err)
	}
	// The prior schema's account/session, token, and publication still work.
	if page := d.b.Send("GET", "/bots/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), `action="/bots/1/pause"`) {
		t.Fatal("migration lost owner access or changed the default pause state")
	}
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	if sent := waitSent(t, d.f, d.Sent); sent[len(sent)-1].Text != "نام شما چیست؟" {
		t.Fatal("migration lost credentials or the Published flow")
	}
}

func TestBotPausePersistsThroughReactivationAndRejectsModeMismatch(t *testing.T) {
	d := newInquiryDriver(t)
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := d.b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), `data-bot-state="paused"`) {
		t.Fatal("reactivation silently resumed a paused Bot")
	}
	if err := d.a.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := d.a.cfg
	cfg.BotPublicURL = ""
	restarted, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	d.b.Router = restarted.server.Handler
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 409 {
		t.Fatalf("resumed incompatible receiver: %d", got.Code)
	}
	if page := d.b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "روش دریافت این سرور تغییر کرده") {
		t.Fatal("incompatible receiver status missing")
	}
}

func TestBotPauseStorageFailurePreservesExistingState(t *testing.T) {
	d := newInquiryDriver(t)
	if _, err := d.a.db.Exec(`CREATE TRIGGER reject_pause BEFORE UPDATE OF paused ON bots BEGIN SELECT RAISE(ABORT,'injected storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 500 {
		t.Fatal(got.Code)
	}
	if page := d.b.Send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), `data-bot-state="paused"`) || !strings.Contains(page.Body.String(), `action="/bots/1/pause"`) {
		t.Fatal("failed pause changed persisted state")
	}
}
