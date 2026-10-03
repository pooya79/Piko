package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
)

// Use the confirmed HTTP + migrated SQLite + fake Telegram seam, including Run.
func pollingFixture(t *testing.T) (*App, *accountBrowser, *telegramFake) {
	t.Helper()
	a, b, f := deliveryFixture(t)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := a.cfg
	cfg.Environment, cfg.BotPublicURL = "development", ""
	local, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.db.Close() })
	b.router = local.server.Handler
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
	production, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	b.router = production.server.Handler
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "روش دریافت این سرور تغییر کرده") {
		t.Error("saved activation incorrectly presented as operating in this server mode")
	}
	stop := runDeliveryApp(t, production)
	// Observe several worker ticks: saved activation must not silently change modes.
	time.Sleep(500 * time.Millisecond)
	stop()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.offsets) != 0 {
		t.Fatal("webhook server claimed a development polling receiver")
	}
}

func activatePolling(t *testing.T, b *accountBrowser) {
	t.Helper()
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatalf("activate polling: %d", got.Code)
	}
}

func TestDevelopmentUsesBoundedLongPolling(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	f.mu.Lock()
	f.requireLongPoll = true
	f.pollingUpdates = []json.RawMessage{json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`)}
	f.mu.Unlock()
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
		page := b.send("GET", path, nil)
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

func waitOffset(t *testing.T, f *telegramFake, want int64) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		offsets := append([]int64(nil), f.offsets...)
		f.mu.Unlock()
		for _, offset := range offsets {
			if offset == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Telegram never received durable acknowledgement offset %d", want)
}

func waitPollingError(t *testing.T, b *accountBrowser) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		page := b.send("GET", "/bots/1", nil)
		if page.Code == 200 && strings.Contains(page.Body.String(), "دریافت محلی یا تحویل پیام با خطا") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("polling failure was not visible to owner")
}

func restartPolling(t *testing.T, a *App, b *accountBrowser, f *telegramFake) *App {
	t.Helper()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	return restarted
}

func TestPollingAcknowledgementAndParticipantStateSurviveRestart(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	const start = `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`
	f.mu.Lock()
	// Duplicate receipt within a batch must not duplicate the runtime transition.
	f.pollingUpdates = []json.RawMessage{json.RawMessage(start), json.RawMessage(start)}
	f.mu.Unlock()
	stop := runDeliveryApp(t, a)
	sent := waitSent(t, f, 2)
	waitOffset(t, f, 101)
	stop()
	f.mu.Lock()
	beforeRestart := len(f.offsets)
	f.pollingUpdates = append(f.pollingUpdates, json.RawMessage(callbackPayload(101, "poll-choice", 77, sent[1].Markup.Buttons[0][0].Data)))
	f.mu.Unlock()
	restarted := restartPolling(t, a, b, f)
	stop = runDeliveryApp(t, restarted)
	sent = waitSent(t, f, 4)
	waitOffset(t, f, 102)
	stop()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offsets[beforeRestart] != 101 {
		t.Fatal("restart reset acknowledged Telegram progress")
	}
	if len(f.sent) != 4 || sent[2].Text != "Open 9 to 5" || len(f.answers) != 1 {
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
	f.mu.Lock()
	f.pollingUpdates = []json.RawMessage{
		json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`),
		json.RawMessage(`{"update_id":101,"message":{"from":{"id":88},"chat":{"id":88,"type":"private"},"text":"/start"}}`),
	}
	f.mu.Unlock()
	stop := runDeliveryApp(t, a)
	waitPollingError(t, b)
	f.mu.Lock()
	for _, offset := range f.offsets {
		if offset != 0 {
			t.Error("Telegram acknowledged an unstored update")
		}
	}
	f.mu.Unlock()
	if _, err := a.db.Exec(`DROP TRIGGER reject_polling`); err != nil {
		t.Fatal(err)
	}
	stop()
	restarted := restartPolling(t, a, b, f)
	stop = runDeliveryApp(t, restarted)
	sent := waitSentWithin(t, f, 4, 8*time.Second)
	waitOffset(t, f, 102)
	stop()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 4 || sent[0].ChatID != 77 || sent[2].ChatID != 88 {
		t.Fatal("partial batch was lost or replayed after restart")
	}
}

func TestPollingAPIFailuresRemainVisibleAndRetryWithoutResettingOffset(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusServiceUnavailable, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			a, b, f := pollingFixture(t)
			activatePolling(t, b)
			f.mu.Lock()
			f.pollFailures, f.pollStatus = 1, status
			f.pollingUpdates = []json.RawMessage{json.RawMessage(`{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`)}
			f.mu.Unlock()
			stop := runDeliveryApp(t, a)
			waitPollingError(t, b)
			stop()
			restarted := restartPolling(t, a, b, f)
			stop = runDeliveryApp(t, restarted)
			waitSentWithin(t, f, 2, 8*time.Second)
			waitOffset(t, f, 101)
			if page := b.send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), "دریافت محلی یا تحویل پیام با خطا") {
				t.Fatal("recovered polling still reports failure")
			}
			stop()
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.offsets) < 3 || f.offsets[0] != 0 || f.offsets[1] != 0 {
				t.Fatal("API failure acknowledged an update")
			}
			for _, call := range f.calls {
				if call == "setWebhook" {
					t.Error("polling failure changed remote delivery")
				}
			}
		})
	}
}

func TestPollingActivationRechecksForeignWebhookAndPreservesUpdates(t *testing.T) {
	_, b, f := pollingFixture(t)
	f.mu.Lock()
	f.webhook = "https://foreign.example.test/secret-one"
	f.mu.Unlock()
	page := b.send("GET", "/bots/1/activate", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "ربات جداگانه") || strings.Contains(page.Body.String(), "secret-one") {
		t.Fatal("local activation guidance or conflict confidentiality missing")
	}
	confirmation := hiddenValue(t, page.Body.String(), "conflict")
	f.mu.Lock()
	if len(f.offsets) != 0 {
		t.Error("published Bot received updates before activation")
	}
	for _, call := range f.calls {
		if call == "deleteWebhook" || call == "setWebhook" {
			t.Error("inspection mutated delivery")
		}
	}
	f.webhook = "https://foreign.example.test/secret-two"
	f.mu.Unlock()
	got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {confirmation}})
	if got.Code != 409 {
		t.Fatalf("changed webhook: %d", got.Code)
	}
	confirmation = hiddenValue(t, got.Body.String(), "conflict")
	f.mu.Lock()
	if f.webhook != "https://foreign.example.test/secret-two" {
		t.Error("unconfirmed webhook was deleted")
	}
	f.activationFails = true
	f.mu.Unlock()
	values := url.Values{"operate": {"yes"}, "conflict": {confirmation}}
	if got := b.post("/bots/1/activate", values); got.Code != 503 {
		t.Fatalf("failed webhook removal: %d", got.Code)
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "فعال\u200cسازی ناموفق") {
		t.Fatal("local activation failure not persisted")
	}
	f.mu.Lock()
	f.activationFails = false
	f.mu.Unlock()
	if got := b.post("/bots/1/activate", values); got.Code != 303 {
		t.Fatalf("retry activation: %d", got.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.webhook != "" {
		t.Fatal("explicit polling activation did not remove webhook")
	}
}

func TestPollingLeaseAndCancellationAcrossServerProcesses(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	f.mu.Lock()
	f.holdPoll = true
	f.pollStarted = make(chan struct{}, 4)
	f.pollCancelled = make(chan struct{}, 4)
	started, cancelled := f.pollStarted, f.pollCancelled
	f.mu.Unlock()
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
