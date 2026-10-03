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
)

type telegramFake struct {
	mu                sync.Mutex
	webhook, secret   string
	activationFails   bool
	calls             []string
	sent              []telegram.SendMessage
	answers           []string
	url               string
	sendFailures      int
	sendStarted       chan struct{}
	holdSend          bool
	pollingUpdates    []json.RawMessage
	offsets           []int64
	blockedChat       int64
	blockedStatus     int
	holdActivation    bool
	activationStarted chan struct{}
}

func (f *telegramFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.calls = append(f.calls, method)
	if method == "getMe" || method == "getWebhookInfo" {
		var params map[string]any
		if json.NewDecoder(r.Body).Decode(&params) != nil || params == nil {
			w.WriteHeader(400)
			return
		}
	}
	switch method {
	case "getMe":
		fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Live Bot","username":"live_bot"}}`)
	case "getWebhookInfo":
		fmt.Fprintf(w, `{"ok":true,"result":{"url":%q,"pending_update_count":7}}`, f.webhook)
	case "setWebhook":
		var p struct {
			URL, Secret string
			Drop        bool
		}
		var raw struct {
			URL    string `json:"url"`
			Secret string `json:"secret_token"`
			Drop   bool   `json:"drop_pending_updates"`
		}
		if json.NewDecoder(r.Body).Decode(&raw) != nil {
			w.WriteHeader(400)
			return
		}
		p.URL, p.Secret, p.Drop = raw.URL, raw.Secret, raw.Drop
		if f.holdActivation {
			close(f.activationStarted)
			<-r.Context().Done()
			return
		}
		if p.Drop {
			w.WriteHeader(400)
			return
		}
		if f.activationFails {
			w.WriteHeader(503)
			return
		}
		f.webhook, f.secret = p.URL, p.Secret
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "sendMessage":
		var p telegram.SendMessage
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			w.WriteHeader(400)
			return
		}
		if f.holdSend {
			close(f.sendStarted)
			<-r.Context().Done()
			return
		}
		if f.sendFailures > 0 {
			f.sendFailures--
			w.WriteHeader(503)
			return
		}
		if p.ChatID == f.blockedChat {
			status := f.blockedStatus
			if status == 0 {
				status = 403
			}
			w.WriteHeader(status)
			return
		}
		f.sent = append(f.sent, p)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	case "answerCallbackQuery":
		var p struct {
			ID string `json:"callback_query_id"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			w.WriteHeader(400)
			return
		}
		f.answers = append(f.answers, p.ID)
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "deleteWebhook":
		var p struct {
			Drop bool `json:"drop_pending_updates"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.Drop {
			w.WriteHeader(400)
			return
		}
		f.webhook = ""
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "getUpdates":
		var p struct {
			Offset int64 `json:"offset"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			w.WriteHeader(400)
			return
		}
		f.offsets = append(f.offsets, p.Offset)
		updates := []json.RawMessage{}
		for _, data := range f.pollingUpdates {
			var u struct {
				ID int64 `json:"update_id"`
			}
			_ = json.Unmarshal(data, &u)
			if u.ID >= p.Offset {
				updates = append(updates, data)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
	default:
		w.WriteHeader(500)
	}
}

func webhook(a *App, secret, payload string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/telegram/bots/1", strings.NewReader(payload))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, r)
	return w
}
func waitSent(t *testing.T, f *telegramFake, count int) []telegram.SendMessage {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		sent := append([]telegram.SendMessage(nil), f.sent...)
		f.mu.Unlock()
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
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 2 {
		t.Fatal("update retry duplicated output")
	}
}

func TestShutdownCancelsTelegramBeforeClosingStorageAndRecoversOutput(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.holdSend = true
	f.sendStarted = make(chan struct{})
	started := f.sendStarted
	f.mu.Unlock()
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
	f.mu.Lock()
	f.holdSend = false
	f.mu.Unlock()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.url, http.DefaultClient))
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
	local, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.db.Close() })
	b.router = local.server.Handler
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	// A valid Telegram backlog larger than 64 KiB must still enter the inbox.
	for i := range 20 {
		f.pollingUpdates = append(f.pollingUpdates, json.RawMessage(fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":%d},"chat":{"id":%d,"type":"private"},"text":%q}}`, 101+i, 77+i, 77+i, "/start "+strings.Repeat("x", 4000))))
	}
	f.mu.Unlock()
	stop := runDeliveryApp(t, local)
	sent := waitSent(t, f, 2)
	if sent[0].Text != "Hello" || sent[1].Markup == nil {
		t.Fatal("polling did not use the shared runtime")
	}
	stop()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.offsets) == 0 || f.offsets[0] != 0 {
		t.Fatal("invalid initial polling offset")
	}
}

func TestOwnerDeliveryMutationsRequireAuthorizationAndCSRF(t *testing.T) {
	a, b, _ := deliveryFixture(t)
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("other-delivery@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/publish", "/bots/1/activate"} {
		if got := other.post(path, url.Values{"operate": {"yes"}}); got.Code != 404 {
			t.Fatalf("other owner: %s %d", path, got.Code)
		}
		if got := b.send("POST", path, url.Values{"operate": {"yes"}}); got.Code != 403 {
			t.Fatalf("missing CSRF: %s %d", path, got.Code)
		}
		if got := b.send("GET", path, nil); path == "/bots/1/publish" && got.Code != 405 {
			t.Fatalf("GET publication: %d", got.Code)
		}
	}
}

func TestActivationInspectionLeavesPersistedObservationUnchanged(t *testing.T) {
	_, b, f := deliveryFixture(t)
	f.mu.Lock()
	f.webhook = "https://foreign.example.test/secret"
	f.mu.Unlock()
	if got := b.send("GET", "/bots/1/activate", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "وب\u200cهوک سرویس دیگری") {
		t.Fatal("fresh activation inspection missing")
	}
	if got := b.send("GET", "/bots/1", nil); strings.Contains(got.Body.String(), "از قبل تنظیم شده") {
		t.Fatal("GET changed the saved observation")
	}
}

func TestBlockedRecipientDoesNotStopOtherParticipants(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.blockedChat = 77
	f.mu.Unlock()
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
	if got := b.send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), "تحویل پیام با خطا") {
		t.Fatal("terminal delivery failure invisible")
	}
	stop()
}

func TestTemporaryFailurePreservesParticipantOrderAndLetsOthersContinue(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.blockedChat = 77
	f.blockedStatus = 503
	f.mu.Unlock()
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
	edited := strings.ReplaceAll(structuredDraft, "Hello", "New welcome")
	if got := b.post("/bots/1/draft", url.Values{"definition": {edited}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	f.blockedChat = 0
	f.mu.Unlock()
	sent = waitSent(t, f, 6)
	if sent[2].ChatID != 77 || sent[2].Text != "Hello" || sent[4].Text != "New welcome" {
		t.Fatal("Participant updates or durable outputs were reordered")
	}
	stop()
}

func TestShutdownJoinsInFlightOwnerActivation(t *testing.T) {
	a, b, f := deliveryFixture(t)
	f.mu.Lock()
	f.holdActivation = true
	f.activationStarted = make(chan struct{})
	started := f.activationStarted
	f.mu.Unlock()
	stop := runDeliveryApp(t, a)
	done := make(chan int, 1)
	go func() { done <- b.post("/bots/1/activate", url.Values{"operate": {"yes"}}).Code }()
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
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.mu.Unlock()
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 2 {
		t.Fatal("unsupported chats emitted messages")
	}
}

func deliveryFixture(t *testing.T) (*App, *accountBrowser, *telegramFake) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := &telegramFake{}
	endpoint := httptest.NewServer(fake)
	fake.url = endpoint.URL
	t.Cleanup(endpoint.Close)
	cfg := Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "delivery-test-session-secret-at-least-32", BotEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), BotPublicURL: "https://piko.example.test", LogLevel: "error", ShutdownPeriod: time.Second}
	a, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(endpoint.URL, endpoint.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	b := newAccountBrowser(t, a.server.Handler)
	b.send("GET", "/register", nil)
	if got := b.post("/register", registerValues("delivery@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/draft", url.Values{"definition": {structuredDraft}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, fake
}

func callbackPayload(updateID int, query string, participant int, data string) string {
	return fmt.Sprintf(`{"update_id":%d,"callback_query":{"id":%q,"from":{"id":%d},"message":{"message_id":2,"chat":{"id":%d,"type":"private"}},"data":%q}}`, updateID, query, participant, participant, data)
}
func waitAnswers(t *testing.T, f *telegramFake, count int) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := len(f.answers)
		f.mu.Unlock()
		if n >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("callback was not acknowledged")
}
func TestPublishedEditsAndObsoleteCallbacksLeaveParticipantVersionIntact(t *testing.T) {
	a, b, f := deliveryFixture(t)
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.mu.Unlock()
	stop := runDeliveryApp(t, a)
	if got := webhook(a, secret, `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	sent := waitSent(t, f, 2)
	button := sent[1].Markup.Buttons[0][0].Data
	edited := strings.ReplaceAll(strings.ReplaceAll(structuredDraft, "Hello", "New welcome"), "Open 9 to 5", "New hours")
	if got := b.post("/bots/1/draft", url.Values{"definition": {edited}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
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
	f.mu.Lock()
	if len(f.sent) != 4 {
		t.Error("obsolete or wrong-Participant callback changed behavior")
	}
	f.mu.Unlock()
	// A fresh Start after restart follows the newest immutable version.
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.url, http.DefaultClient))
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
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.sendFailures = 1
	f.mu.Unlock()
	start := `{"update_id":100,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`
	if got := webhook(a, secret, start); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	stop := runDeliveryApp(t, restarted)
	b.router = restarted.server.Handler
	deadline := time.Now().Add(3 * time.Second)
	visible := false
	for time.Now().Before(deadline) {
		if page := b.send("GET", "/bots/1", nil); strings.Contains(page.Body.String(), "تحویل پیام با خطا") {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) != 2 {
		t.Fatal("completed update replayed")
	}
}

func TestActivationRechecksConflictAndRetriesWithoutDroppingUpdates(t *testing.T) {
	_, b, f := deliveryFixture(t)
	f.mu.Lock()
	f.webhook = "https://foreign.example.test/hidden-secret"
	f.mu.Unlock()
	got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}})
	if got.Code != 409 || strings.Contains(got.Body.String(), "hidden-secret") {
		t.Fatalf("fresh conflict: %d", got.Code)
	}
	confirmation := hiddenValue(t, got.Body.String(), "conflict")
	f.mu.Lock()
	f.activationFails = true
	f.mu.Unlock()
	got = b.post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {confirmation}})
	if got.Code != 503 {
		t.Fatalf("failure: %d", got.Code)
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "فعال\u200cسازی ناموفق") {
		t.Fatal("failure state not persisted")
	}
	f.mu.Lock()
	f.activationFails = false
	f.mu.Unlock()
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {confirmation}}); got.Code != 303 {
		t.Fatalf("retry: %d", got.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.webhook != "https://piko.example.test/telegram/bots/1" || f.secret == "" || f.secret == testBotToken {
		t.Fatal("webhook endpoint or separate authentication missing")
	}
}

func hiddenValue(t *testing.T, html, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	_, tail, ok := strings.Cut(html, marker)
	if !ok {
		t.Fatalf("missing %s", name)
	}
	value, _, _ := strings.Cut(tail, `"`)
	return value
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
