package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/testsupport"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func webhook(a *App, secret, payload string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/telegram/bots/1", strings.NewReader(payload))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, r)
	return w
}

func waitSent(t *testing.T, f *fixture.TelegramFake, count int) []telegram.SendMessage {
	return waitSentWithin(t, f, count, 4*time.Second)
}

func waitSentWithin(t *testing.T, f *fixture.TelegramFake, count int, timeout time.Duration) []telegram.SendMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.Mu.Lock()
		sent := append([]telegram.SendMessage(nil), f.Sent...)
		f.Mu.Unlock()
		if len(sent) >= count {
			return sent
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Telegram received fewer than %d messages", count)
	return nil
}

func TestWebhookDurablyAcceptsAndDeduplicatesPrivateStart(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.Mu.Unlock()
	const start = `{"update_id":100,"message":{"message_id":1,"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`
	for _, invalid := range []string{"", "wrong"} {
		if got := webhook(a, invalid, start); got.Code != 401 {
			t.Fatalf("unauthorized: %d", got.Code)
		}
	}
	for range 2 {
		got := webhook(a, secret, start)
		if got.Code != 200 || len(got.Result().Cookies()) != 0 {
			t.Fatalf("durable machine acceptance: %d", got.Code)
		}
	}
	stop := runDeliveryApp(t, a)
	sent := waitSent(t, f, 2)
	if sent[0].Text != "Hello" || sent[1].Text != "Choose" || sent[1].Markup == nil {
		t.Fatal("live Start differs from Preview")
	}
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if len(f.Sent) != 2 {
		t.Fatal("update retry duplicated output")
	}
}

func TestShutdownCancelsTelegramBeforeClosingStorageAndRecoversOutput(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.HoldSend = true
	f.SendStarted = make(chan struct{})
	started := f.SendStarted
	f.Mu.Unlock()
	if got := webhook(a, secret, `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	stop := runDeliveryApp(t, a)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("send never started")
	}
	stop()
	if err := a.db.Ping(); err == nil {
		t.Fatal("Run returned before database close")
	}
	f.Mu.Lock()
	f.HoldSend = false
	f.Mu.Unlock()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	runDeliveryApp(t, restarted)
	sent := waitSent(t, f, 2)
	if sent[0].Text != "Hello" {
		t.Fatal("staged output lost on cancellation")
	}
}

func TestPollingUsesDurableOffsetsAndSameRuntime(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	cfg.BotPublicURL = ""
	local, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.db.Close() })
	b.Router = local.server.Handler
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	// A valid Telegram backlog larger than 64 KiB must still enter the inbox.
	for i := range 20 {
		f.PollingUpdates = append(f.PollingUpdates, json.RawMessage(fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":%d},"chat":{"id":%d,"type":"private"},"text":%q}}`, 101+i, 77+i, 77+i, "/start "+strings.Repeat("x", 4000))))
	}
	f.Mu.Unlock()
	stop := runDeliveryApp(t, local)
	sent := waitSent(t, f, 2)
	if sent[0].Text != "Hello" || sent[1].Markup == nil {
		t.Fatal("polling did not use the shared runtime")
	}
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if len(f.Offsets) == 0 || f.Offsets[0] != 0 {
		t.Fatal("invalid initial polling offset")
	}
}

func TestBlockedRecipientDoesNotStopOtherParticipants(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.BlockedChat = 77
	f.Mu.Unlock()
	for i, participant := range []int{77, 88} {
		data := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":%d},"chat":{"id":%d,"type":"private"},"text":"/start"}}`, 100+i, participant, participant)
		if got := webhook(a, secret, data); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
	stop := runDeliveryApp(t, a)
	sent := waitSent(t, f, 2)
	if sent[0].ChatID != 88 || sent[0].Text != "Hello" {
		t.Fatal("blocked recipient stopped another Participant")
	}
	if got := b.Send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), "تحویل پیام با خطا") {
		t.Fatal("terminal delivery failure invisible")
	}
	stop()
}

func TestTemporaryFailurePreservesParticipantOrderAndLetsOthersContinue(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.BlockedChat = 77
	f.BlockedStatus = 503
	f.Mu.Unlock()
	for i, participant := range []int{77, 77, 88} {
		data := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":%d},"chat":{"id":%d,"type":"private"},"text":"/start"}}`, 100+i, participant, participant)
		if got := webhook(a, secret, data); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
	stop := runDeliveryApp(t, a)
	sent := waitSent(t, f, 2)
	if sent[0].ChatID != 88 {
		t.Fatal("temporary failure stopped another Participant")
	}
	edited := strings.ReplaceAll(fixture.StructuredDraft, "Hello", "New welcome")
	if err := b.SaveDraft(t, 1, url.Values{"definition": {edited}}); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	f.BlockedChat = 0
	f.Mu.Unlock()
	sent = waitSent(t, f, 6)
	if sent[2].ChatID != 77 || sent[2].Text != "Hello" || sent[4].Text != "New welcome" {
		t.Fatal("Participant updates or durable outputs were reordered")
	}
	stop()
}

func TestShutdownJoinsInFlightOwnerActivation(t *testing.T) {
	a, b, f := deliveryFixture(t)
	f.Mu.Lock()
	f.HoldActivation = true
	f.ActivationStarted = make(chan struct{})
	started := f.ActivationStarted
	f.Mu.Unlock()
	stop := runDeliveryApp(t, a)
	done := make(chan int, 1)
	go func() { done <- b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}).Code }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("activation never started")
	}
	stop()
	select {
	case status := <-done:
		if status != 503 {
			t.Fatalf("cancelled activation: %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("database closed before activation handler finished")
	}
	if err := a.db.Ping(); err == nil {
		t.Fatal("storage not closed")
	}
}

func TestWebhookStorageFailureIsNotAcknowledgedAndUnsupportedChatsStaySilent(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.Mu.Unlock()
	if _, err := a.db.Exec(`CREATE TRIGGER reject_delivery BEFORE INSERT ON bot_updates BEGIN SELECT RAISE(ABORT,'injected storage failure');END`); err != nil {
		t.Fatal(err)
	}
	const start = `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`
	if got := webhook(a, secret, start); got.Code != 503 {
		t.Fatalf("uncommitted delivery acknowledged: %d", got.Code)
	}
	if _, err := a.db.Exec(`DROP TRIGGER reject_delivery`); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"group", "supergroup", "channel"} {
		p := fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":77},"chat":{"id":77,"type":%q},"text":"/start"}}`, i+1, kind)
		if got := webhook(a, secret, p); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
	if got := webhook(a, secret, start); got.Code != 200 {
		t.Fatal(got.Code)
	}
	stop := runDeliveryApp(t, a)
	waitSent(t, f, 2)
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if len(f.Sent) != 2 {
		t.Fatal("unsupported chats emitted messages")
	}
}

func deliveryFixture(t *testing.T) (*App, *fixture.Browser, *fixture.TelegramFake) {
	t.Helper()
	return deliveryFixtureClock(t, time.Now)
}

func deliveryFixtureClock(t *testing.T, now func() time.Time) (*App, *fixture.Browser, *fixture.TelegramFake) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := &fixture.TelegramFake{}
	endpoint := httptest.NewServer(fake)
	fake.URL = endpoint.URL
	t.Cleanup(endpoint.Close)
	cfg := Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "delivery-test-session-secret-at-least-32", BotEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), BotPublicURL: "https://piko.example.test", LogLevel: "error", ShutdownPeriod: time.Second}
	a, err := newWithTelegramClock(t.Context(), cfg, telegram.NewClient(endpoint.URL, endpoint.Client()), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	b := fixture.NewAccountBrowser(t, a.server.Handler, a.cfg.DatabasePath)
	b.Send("GET", "/register", nil)
	if got := b.Post("/register", fixture.RegisterValues("delivery@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if err := b.SaveDraft(t, 1, url.Values{"definition": {fixture.StructuredDraft}}); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, fake
}

func callbackPayload(updateID int, query string, participant int, data string) string {
	return fmt.Sprintf(`{"update_id":%d,"callback_query":{"id":%q,"from":{"id":%d},"message":{"message_id":2,"chat":{"id":%d,"type":"private"}},"data":%q}}`, updateID, query, participant, participant, data)
}

func waitAnswers(t *testing.T, f *fixture.TelegramFake, count int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		f.Mu.Lock()
		n := len(f.Answers)
		f.Mu.Unlock()
		if n >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("callback was not acknowledged")
}

func TestPublishedEditsAndObsoleteCallbacksLeaveParticipantVersionIntact(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.Mu.Unlock()
	stop := runDeliveryApp(t, a)
	if got := webhook(a, secret, `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	sent := waitSent(t, f, 2)
	button := sent[1].Markup.Buttons[0][0].Data
	edited := strings.ReplaceAll(strings.ReplaceAll(fixture.StructuredDraft, "Hello", "New welcome"), "Open 9 to 5", "New hours")
	if err := b.SaveDraft(t, 1, url.Values{"definition": {edited}}); err != nil {
		t.Fatal(err)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for range 2 {
		if got := webhook(a, secret, callbackPayload(101, "choice", 77, button)); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
	sent = waitSent(t, f, 4)
	if sent[2].Text != "Open 9 to 5" {
		t.Fatal("publication changed existing Participant version")
	}
	// Same callback ID in another update, obsolete buttons, and another Participant
	// are acknowledged without additional replies or state changes.
	for i, p := range []string{callbackPayload(102, "choice", 77, button), callbackPayload(103, "old", 77, button), callbackPayload(104, "wrong", 88, sent[3].Markup.Buttons[0][0].Data)} {
		if got := webhook(a, secret, p); got.Code != 200 {
			t.Fatal(got.Code)
		}
		waitAnswers(t, f, i+2)
	}
	stop()
	f.Mu.Lock()
	if len(f.Sent) != 4 {
		t.Error("obsolete or wrong-Participant callback changed behavior")
	}
	f.Mu.Unlock()
	// A fresh Start after restart follows the newest immutable version.
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	runDeliveryApp(t, restarted)
	if got := webhook(restarted, secret, `{"update_id":105,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	sent = waitSent(t, f, 6)
	if sent[4].Text != "New welcome" {
		t.Fatal("new Start ignored latest publication")
	}
}

func TestAcceptedUpdatesRecoverAfterRestartAndOutboundFailure(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.Mu.Lock()
	secret := f.Secret
	f.SendFailures = 1
	f.Mu.Unlock()
	start := `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`
	if got := webhook(a, secret, start); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	stop := runDeliveryApp(t, restarted)
	b.Router = restarted.server.Handler
	deadline := time.Now().Add(3 * time.Second)
	visible := false
	for time.Now().Before(deadline) {
		if page := b.Send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), "تحویل پیام با خطا") {
			visible = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !visible {
		t.Fatal("outbound failure was not visible to owner")
	}
	sent := waitSent(t, f, 2)
	if sent[0].Text != "Hello" {
		t.Fatal("accepted update lost on restart")
	}
	if got := webhook(restarted, secret, start); got.Code != 200 {
		t.Fatal(got.Code)
	}
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if len(f.Sent) != 2 {
		t.Fatal("completed update replayed")
	}
}

// Workers are exercised through the same server lifecycle used in production.
func runDeliveryApp(t *testing.T, a *App) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("shutdown: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	})
	t.Cleanup(stop)
	return stop
}
