package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderFormReplacementPreservesPublishedInteractionsAndPrivateAnswers(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("PRIVATE-PARTICIPANT-ANSWER", 1)
	stop()
	var calls atomic.Int64
	provider := httptest.NewServer(fixture.StreamingBuilderProvider(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		for _, secret := range []string{"PRIVATE-PARTICIPANT-ANSWER", fixture.TestBotToken, d.Secret} {
			if strings.Contains(string(body), secret) {
				t.Error("model context exposed live answers or Telegram credentials")
			}
		}
		switch calls.Add(1) {
		case 1:
			fixture.BuilderToolReply(w, "read_draft", map[string]any{})
		case 2:
			if !strings.Contains(string(body), "شماره تماس شما چیست؟") {
				t.Error("authorized Draft snapshot missing")
			}
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
		case 3:
			fixture.BuilderTextReply(w)
		}
	}))
	t.Cleanup(provider.Close)
	cfg := d.a.cfg
	cfg.Builder = builder.Config{APIKey: "test-server-key", BaseURL: provider.URL + "/v1"}
	restarted, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.Router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	if got := d.b.Post("/bots/1/chats", url.Values{"title": {"فرم تازه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/chats/1/messages", url.Values{"message": {"فرم قدیمی را با فرم تازه جایگزین کن"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, d.b, "/bots/1/chats/1", "succeeded")
	if got := d.b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("unfinished Interaction switched to the generated Form")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	page := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String()
	if !strings.Contains(page, "PRIVATE-PARTICIPANT-ANSWER") || strings.Contains(page, "مقدار") || !strings.Contains(page, "تماس") {
		t.Fatal("Submission lost its frozen Form or answers")
	}
	d.press("شروع دوباره", 2)
	d.press("فرم دلخواه", 1)
	sent = waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "نام شما؟" {
		t.Fatal("new Interaction did not use the newly published Form")
	}
}
