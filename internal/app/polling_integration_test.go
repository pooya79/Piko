package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

// Use the confirmed HTTP + migrated SQLite + fake Telegram seam, including Run.
func pollingFixture(t *testing.T) (*App, *fixture.Browser, *fixture.TelegramFake) {
	t.Helper()
	a, b, f := deliveryFixture(t)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	cfg.Environment, cfg.BotPublicURL = "development", ""
	local, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.db.Close() })
	b.Router = local.server.Handler
	return local, b, f
}

func TestWebhookServerDoesNotResumeDevelopmentPolling(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	cfg.Environment, cfg.BotPublicURL = "production", "https://piko.example.test"
	production, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	b.Router = production.server.Handler
	if page := b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "روش دریافت این سرور تغییر کرده") {
		t.Error("saved activation incorrectly presented as operating in this server mode")
	}
	stop := runDeliveryApp(t, production)
	// Observe several worker ticks: saved activation must not silently change modes.
	time.Sleep(500 * time.Millisecond)
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if len(f.Offsets) != 0 {
		t.Fatal("webhook server claimed a development polling receiver")
	}
}

func activatePolling(t *testing.T, b *fixture.Browser) {
	t.Helper()
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatalf("activate polling: %d", got.Code)
	}
}

func TestDevelopmentUsesBoundedLongPolling(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	f.Mu.Lock()
	f.RequireLongPoll = true
	f.PollingUpdates = []json.RawMessage{json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`)}
	f.Mu.Unlock()
	stop := runDeliveryApp(t, a)
	sent := waitSent(t, f, 2)
	if sent[0].Text != "Hello" || sent[1].Text != "Choose" {
		t.Fatal("polling differed from the shared published runtime")
	}
	stop()
}

func TestOwnerSeesSavedPollingMode(t *testing.T) {
	_, b, _ := pollingFixture(t)
	activatePolling(t, b)
	for _, path := range []string{"/bots", "/bots/1"} {
		page := b.Send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), "دریافت محلی با polling") {
			t.Fatalf("saved local delivery mode missing on %s", path)
		}
	}
}

func waitPoll(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("polling request did not arrive")
	}
}

func waitOffset(t *testing.T, f *fixture.TelegramFake, want int64) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		f.Mu.Lock()
		offsets := append([]int64(nil), f.Offsets...)
		f.Mu.Unlock()
		for _, offset := range offsets {
			if offset == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Telegram never received durable acknowledgement offset %d", want)
}

func waitPollingError(t *testing.T, b *fixture.Browser) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		page := b.Send("GET", "/bots/1", nil)
		if page.Code == 200 && strings.Contains(page.Body.String(), "دریافت محلی یا تحویل پیام با خطا") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("polling failure was not visible to owner")
}

func restartPolling(t *testing.T, a *App, b *fixture.Browser, f *fixture.TelegramFake) *App {
	t.Helper()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.Router = restarted.server.Handler
	return restarted
}

func TestPollingAcknowledgementAndParticipantStateSurviveRestart(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	const start = `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`
	f.Mu.Lock()
	// Duplicate receipt within a batch must not duplicate the runtime transition.
	f.PollingUpdates = []json.RawMessage{json.RawMessage(start), json.RawMessage(start)}
	f.Mu.Unlock()
	stop := runDeliveryApp(t, a)
	sent := waitSent(t, f, 2)
	waitOffset(t, f, 101)
	stop()
	f.Mu.Lock()
	beforeRestart := len(f.Offsets)
	f.PollingUpdates = append(f.PollingUpdates, json.RawMessage(callbackPayload(101, "poll-choice", 77, sent[1].Markup.Buttons[0][0].Data)))
	f.Mu.Unlock()
	restarted := restartPolling(t, a, b, f)
	stop = runDeliveryApp(t, restarted)
	sent = waitSent(t, f, 4)
	waitOffset(t, f, 102)
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Offsets[beforeRestart] != 101 {
		t.Fatal("restart reset acknowledged Telegram progress")
	}
	if len(f.Sent) != 4 || sent[2].Text != "Open 9 to 5" || len(f.Answers) != 1 {
		t.Fatal("duplicate receipt or restart changed Participant behavior")
	}
}

func TestPollingStorageFailureDoesNotAcknowledgeUnstoredBatch(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	// Inject failure at the storage boundary, after the first durable receipt.
	if _, err := a.db.Exec(`CREATE TRIGGER reject_polling BEFORE INSERT ON bot_updates WHEN NEW.update_id=101 BEGIN SELECT RAISE(ABORT,'injected storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.Mu.Lock()
	f.PollingUpdates = []json.RawMessage{
		json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`),
		json.RawMessage(`{"update_id":101,"message":{"from":{"id":88},"chat":{"id":88,"type":"private"},"text":"/start"}}`),
	}
	f.Mu.Unlock()
	stop := runDeliveryApp(t, a)
	waitPollingError(t, b)
	f.Mu.Lock()
	for _, offset := range f.Offsets {
		if offset != 0 {
			t.Error("Telegram acknowledged an unstored update")
		}
	}
	f.Mu.Unlock()
	if _, err := a.db.Exec(`DROP TRIGGER reject_polling`); err != nil {
		t.Fatal(err)
	}
	stop()
	restarted := restartPolling(t, a, b, f)
	stop = runDeliveryApp(t, restarted)
	sent := waitSentWithin(t, f, 4, 8*time.Second)
	waitOffset(t, f, 102)
	stop()
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if len(f.Sent) != 4 || sent[0].ChatID != 77 || sent[2].ChatID != 88 {
		t.Fatal("partial batch was lost or replayed after restart")
	}
}

func TestPollingAPIFailuresRemainVisibleAndRetryWithoutResettingOffset(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusServiceUnavailable, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a, b, f := pollingFixture(t)
			activatePolling(t, b)
			f.Mu.Lock()
			f.PollFailures, f.PollStatus = 1, status
			f.PollingUpdates = []json.RawMessage{json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`)}
			f.Mu.Unlock()
			stop := runDeliveryApp(t, a)
			waitPollingError(t, b)
			stop()
			restarted := restartPolling(t, a, b, f)
			stop = runDeliveryApp(t, restarted)
			waitSentWithin(t, f, 2, 8*time.Second)
			waitOffset(t, f, 101)
			if page := b.Send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), "دریافت محلی یا تحویل پیام با خطا") {
				t.Fatal("recovered polling still reports failure")
			}
			stop()
			f.Mu.Lock()
			defer f.Mu.Unlock()
			if len(f.Offsets) < 3 || f.Offsets[0] != 0 || f.Offsets[1] != 0 {
				t.Fatal("API failure acknowledged an update")
			}
			for _, call := range f.Calls {
				if call == "setWebhook" {
					t.Error("polling failure changed remote delivery")
				}
			}
		})
	}
}

func TestPollingActivationRechecksForeignWebhookAndPreservesUpdates(t *testing.T) {
	_, b, f := pollingFixture(t)
	f.Mu.Lock()
	f.Webhook = "https://foreign.example.test/secret-one"
	f.Mu.Unlock()
	page := b.Send("GET", "/bots/1/activate", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "ربات جداگانه") || strings.Contains(page.Body.String(), "secret-one") {
		t.Fatal("local activation guidance or conflict confidentiality missing")
	}
	confirmation := fixture.HiddenValue(t, page.Body.String(), "conflict")
	f.Mu.Lock()
	if len(f.Offsets) != 0 {
		t.Error("published Bot received updates before activation")
	}
	for _, call := range f.Calls {
		if call == "deleteWebhook" || call == "setWebhook" {
			t.Error("inspection mutated delivery")
		}
	}
	f.Webhook = "https://foreign.example.test/secret-two"
	f.Mu.Unlock()
	got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {confirmation}})
	if got.Code != 409 {
		t.Fatalf("changed webhook: %d", got.Code)
	}
	confirmation = fixture.HiddenValue(t, got.Body.String(), "conflict")
	f.Mu.Lock()
	if f.Webhook != "https://foreign.example.test/secret-two" {
		t.Error("unconfirmed webhook was deleted")
	}
	f.ActivationFails = true
	f.Mu.Unlock()
	values := url.Values{"operate": {"yes"}, "conflict": {confirmation}}
	if got := b.Post("/bots/1/activate", values); got.Code != 503 {
		t.Fatalf("failed webhook removal: %d", got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "فعال\u200cسازی ناموفق") {
		t.Fatal("local activation failure not persisted")
	}
	f.Mu.Lock()
	f.ActivationFails = false
	f.Mu.Unlock()
	if got := b.Post("/bots/1/activate", values); got.Code != 303 {
		t.Fatalf("retry activation: %d", got.Code)
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Webhook != "" {
		t.Fatal("explicit polling activation did not remove webhook")
	}
}

func TestPollingLeaseAndCancellationAcrossServerProcesses(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	f.Mu.Lock()
	f.HoldPoll = true
	f.PollStarted = make(chan struct{}, 4)
	f.PollCancelled = make(chan struct{}, 4)
	started, cancelled := f.PollStarted, f.PollCancelled
	f.Mu.Unlock()
	stopFirst := runDeliveryApp(t, a)
	waitPoll(t, started)
	second := restartPolling(t, a, b, f)
	stopSecond := runDeliveryApp(t, second)
	select {
	case <-started:
		t.Fatal("two Piko processes simultaneously received the same Bot")
	case <-time.After(500 * time.Millisecond):
	}
	stopFirst()
	waitPoll(t, cancelled)
	if err := a.db.Ping(); err == nil {
		t.Fatal("Run did not close storage after joining polling")
	}
	// A clean stop releases the durable lease so the other process can resume.
	waitPoll(t, started)
	stopSecond()
	waitPoll(t, cancelled)
}
