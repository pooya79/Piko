package httpfixture

import (
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

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/testsupport"
)

type TelegramFake struct {
	Mu                sync.Mutex
	Webhook, Secret   string
	ActivationFails   bool
	Calls             []string
	Sent              []telegram.SendMessage
	Answers           []string
	AnswerTexts       []string
	URL               string
	SendFailures      int
	SendStarted       chan struct{}
	HoldSend          bool
	PollingUpdates    []json.RawMessage
	Offsets           []int64
	BlockedChat       int64
	BlockedStatus     int
	HoldActivation    bool
	ActivationStarted chan struct{}
	RequireLongPoll   bool
	PollFailures      int
	PollStatus        int
	HoldPoll          bool
	PollStarted       chan struct{}
	PollCancelled     chan struct{}
	IdentityID        int64
	APIStatus         int
	Tokens            []string
	HoldInspection    bool
	InspectionStarted chan struct{}
	ReleaseInspection chan struct{}
}

func (f *TelegramFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.Calls = append(f.Calls, method)
	parts := strings.Split(r.URL.Path, "/")
	f.Tokens = append(f.Tokens, strings.TrimPrefix(parts[1], "bot"))
	if f.APIStatus != 0 {
		w.WriteHeader(f.APIStatus)
		return
	}
	if method == "getMe" || method == "getWebhookInfo" {
		var params map[string]any
		if json.NewDecoder(r.Body).Decode(&params) != nil || params == nil {
			w.WriteHeader(400)
			return
		}
	}
	switch method {
	case "getMe":
		id := f.IdentityID
		if id == 0 {
			id = 123456
		}
		fmt.Fprintf(w, `{"ok":true,"result":{"id":%d,"is_bot":true,"first_name":"Live Bot","username":"live_bot"}}`, id)
	case "getWebhookInfo":
		if f.HoldInspection {
			f.HoldInspection = false
			started, release := f.InspectionStarted, f.ReleaseInspection
			f.Mu.Unlock()
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
			f.Mu.Lock()
		}
		fmt.Fprintf(w, `{"ok":true,"result":{"url":%q,"pending_update_count":7}}`, f.Webhook)
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
		if f.HoldActivation {
			close(f.ActivationStarted)
			<-r.Context().Done()
			return
		}
		if p.Drop {
			w.WriteHeader(400)
			return
		}
		if f.ActivationFails {
			w.WriteHeader(503)
			return
		}
		f.Webhook, f.Secret = p.URL, p.Secret
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "sendMessage":
		var p telegram.SendMessage
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			w.WriteHeader(400)
			return
		}
		if f.HoldSend {
			close(f.SendStarted)
			<-r.Context().Done()
			return
		}
		if f.SendFailures > 0 {
			f.SendFailures--
			w.WriteHeader(503)
			return
		}
		if p.ChatID == f.BlockedChat {
			status := f.BlockedStatus
			if status == 0 {
				status = 403
			}
			w.WriteHeader(status)
			return
		}
		f.Sent = append(f.Sent, p)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	case "answerCallbackQuery":
		var p struct {
			ID   string `json:"callback_query_id"`
			Text string `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			w.WriteHeader(400)
			return
		}
		f.Answers = append(f.Answers, p.ID)
		f.AnswerTexts = append(f.AnswerTexts, p.Text)
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "deleteWebhook":
		var p struct {
			Drop bool `json:"drop_pending_updates"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.Drop {
			w.WriteHeader(400)
			return
		}
		if f.ActivationFails {
			w.WriteHeader(503)
			return
		}
		f.Webhook = ""
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	case "getUpdates":
		var p struct {
			Offset  int64    `json:"offset"`
			Timeout int      `json:"timeout"`
			Limit   int      `json:"limit"`
			Updates []string `json:"allowed_updates"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			w.WriteHeader(400)
			return
		}
		f.Offsets = append(f.Offsets, p.Offset)
		if f.RequireLongPoll && (p.Timeout <= 0 || p.Timeout >= 10 || p.Limit <= 0 || p.Limit > 100 || strings.Join(p.Updates, ",") != "message,callback_query") {
			w.WriteHeader(400)
			return
		}
		if f.PollStarted != nil {
			select {
			case f.PollStarted <- struct{}{}:
			default:
			}
		}
		if f.HoldPoll {
			cancelled := f.PollCancelled
			f.Mu.Unlock()
			<-r.Context().Done()
			if cancelled != nil {
				cancelled <- struct{}{}
			}
			f.Mu.Lock()
			return
		}
		if f.PollFailures > 0 {
			f.PollFailures--
			w.WriteHeader(f.PollStatus)
			return
		}
		updates := []json.RawMessage{}
		for _, data := range f.PollingUpdates {
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

func Webhook(a *HTTP, secret, payload string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/telegram/bots/1", strings.NewReader(payload))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	return w
}

func DeliveryFixture(t *testing.T) (*HTTP, *Browser, *TelegramFake) {
	t.Helper()
	return DeliveryFixtureClock(t, time.Now)
}

func DeliveryFixtureClock(t *testing.T, now func() time.Time) (*HTTP, *Browser, *TelegramFake) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := &TelegramFake{}
	endpoint := httptest.NewServer(fake)
	fake.URL = endpoint.URL
	t.Cleanup(endpoint.Close)
	cfg := httpapp.Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "delivery-test-session-secret-at-least-32", BotEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), BotPublicURL: "https://piko.example.test", LogLevel: "error", ShutdownPeriod: time.Second}
	a, err := NewWithTelegramClock(t.Context(), cfg, telegram.NewClient(endpoint.URL, endpoint.Client()), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.DB.Close() })
	b := NewAccountBrowser(t, a.Handler)
	b.Send("GET", "/register", nil)
	if got := b.Post("/register", RegisterValues("delivery@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/connect", url.Values{"token": {TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.PostDraft(t, "/bots/1/draft", url.Values{"definition": {StructuredDraft}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, fake
}

func HiddenValue(t *testing.T, html, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	_, tail, ok := strings.Cut(html, marker)
	if !ok {
		t.Fatalf("missing %s", name)
	}
	value, _, _ := strings.Cut(tail, `"`)
	return value
}
