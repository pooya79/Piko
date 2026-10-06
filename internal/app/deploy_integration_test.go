package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/testsupport"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

// Keep all observations at App HTTP and the external Telegram/provider seams.
func deployFixture(t *testing.T, provider http.HandlerFunc) (*App, *fixture.Browser, *fixture.TelegramFake) {
	t.Helper()
	a, b, f := generalDeployFixture(t, provider)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات انتشار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"گفتگوی اول", "گفتگوی دوم"} {
		fixture.SeedBotChat(t, a.db, 1, title)
	}
	if got := b.Post("/bots/1/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, f
}

func generalDeployFixture(t *testing.T, provider http.HandlerFunc) (*App, *fixture.Browser, *fixture.TelegramFake) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	f := &fixture.TelegramFake{}
	api := httptest.NewServer(f)
	t.Cleanup(api.Close)
	cfg := Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "deploy-test-session-secret-at-least-32", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", BotPublicURL: "https://piko.example.test", LogLevel: "error", ShutdownPeriod: time.Second}
	if provider != nil {
		model := httptest.NewServer(fixture.StreamingBuilderProvider(provider))
		t.Cleanup(model.Close)
		cfg.Builder = builder.Config{APIKey: "test-server-key", BaseURL: model.URL + "/v1"}
	}
	a, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(api.URL, api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.stopRequests(); a.builder.Wait(); _ = a.db.Close() })
	b := fixture.NewAccountBrowser(t, a.server.Handler, a.cfg.DatabasePath)
	b.Send("GET", "/register", nil)
	if got := b.Post("/register", fixture.RegisterValues("deploy-owner@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, f
}

func TestDeployRetainsPublishedFlowThroughDraftPreviewUndoAndNewVersions(t *testing.T) {
	var calls atomic.Int64
	a, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 3 {
			fixture.BuilderToolReply(w, "propose_action", map[string]string{"action": "deploy"})
		} else if n == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
		} else {
			fixture.BuilderTextReply(w)
		}
	})
	if err := b.SaveDraft(t, 1, fixture.InquiryDraft()); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.Mu.Unlock()
	d := &formDriver{t: t, a: a, b: b, f: f, Secret: secret, update: 100}
	runDeliveryApp(t, a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("شرکت\u200cکننده پیشین", 1)
	// A generated candidate, Preview and guarded Undo never publish themselves.
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	if got := b.Post("/bots/1/preview", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("Draft/Preview/Undo changed live version")
	}
	updated := fixture.InquiryDraft()
	updated["question_prompt"][1] = "پرسش نسخه تازه"
	updated.Set("acknowledgement", "رسید نسخه تازه")
	if err := b.SaveDraft(t, 1, updated); err != nil {
		t.Fatal(err)
	}
	target := fixture.ProposedAction(t, b, "منتشر کن")
	if got := b.Post(target, url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, f, d.Sent); sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Deploy changed the unfinished Interaction")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	if sent := waitSent(t, f, d.Sent); sent[len(sent)-1].Text != "درخواست شما دریافت شد" {
		t.Fatal("old acknowledgement changed")
	}
	if page := b.Send("GET", "/bots/1/submissions/1", nil).Body.String(); !strings.Contains(page, "شرکت\u200cکننده پیشین") {
		t.Fatal("Submission incompatible")
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("شرکت\u200cکننده تازه", 1)
	if sent := waitSent(t, f, d.Sent); sent[len(sent)-1].Text != "پرسش نسخه تازه" {
		t.Fatal("new Interaction missed deployed version")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if sent := waitSent(t, f, d.Sent); sent[len(sent)-1].Text != "رسید نسخه تازه" {
		t.Fatal("new acknowledgement missing")
	}
}
