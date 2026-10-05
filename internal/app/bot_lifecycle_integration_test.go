package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
)

const replacementToken = "123456:replacementabcdefghijklmnopqrstuvwxyz"

func TestBotLifecycleReplacementFencesAnEarlierActivation(t *testing.T) {
	d := newInquiryDriver(t)
	d.f.mu.Lock()
	d.f.holdInspection = true
	d.f.inspectionStarted = make(chan struct{})
	d.f.releaseInspection = make(chan struct{})
	started, release := d.f.inspectionStarted, d.f.releaseInspection
	d.f.mu.Unlock()
	done := make(chan int, 1)
	go func() { done <- d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}).Code }()
	released := false
	defer func() {
		if !released {
			close(release)
			<-done
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("activation inspection did not start")
	}
	if got := d.b.post("/bots/1/replace-token", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	close(release)
	released = true
	select {
	case status := <-done:
		if status != 409 {
			t.Fatalf("stale activation: %d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("activation did not finish")
	}
	if page := d.b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), `data-bot-state="published.inactive"`) {
		t.Fatal("stale activation resumed replaced Bot")
	}
	if got := d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.f.mu.Lock()
	secret := d.f.secret
	d.f.mu.Unlock()
	if got := webhook(d.a, secret, `{"update_id":950}`); got.Code != 200 {
		t.Fatal("fresh activation installed a stale secret")
	}
}

func TestBotLifecycleAcceptedConfirmationSurvivesDisconnectAndReconnect(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام پذیرفته\u200cشده", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	submit := d.button("ارسال")
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	d.a = restarted
	d.b.router = restarted.server.Handler
	d.update++
	if got := webhook(d.a, d.secret, callbackPayload(d.update, "queued-confirmation", 77, submit)); got.Code != 200 {
		t.Fatal(got.Code)
	}
	for _, action := range []string{"replace-token", "disconnect", "reconnect", "activate"} {
		if got := d.b.post("/bots/1/"+action, url.Values{"token": {replacementToken}, "operate": {"yes"}}); got.Code != 303 {
			t.Fatalf("%s: %d", action, got.Code)
		}
	}
	runDeliveryApp(t, d.a)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		page := d.b.send("GET", "/bots/1/submissions/1", nil)
		if page.Code == 200 && strings.Contains(page.Body.String(), "نام پذیرفته\u200cشده") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("durably accepted confirmation was discarded by lifecycle changes")
}

type lifecycleBody struct {
	reader           io.Reader
	started, release chan struct{}
}

func (b *lifecycleBody) Read(p []byte) (int, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-b.release
	return b.reader.Read(p)
}

func TestBotLifecycleOldWebhookCannotCrossReconnection(t *testing.T) {
	d := newInquiryDriver(t)
	body := &lifecycleBody{reader: strings.NewReader(`{"update_id":900}`), started: make(chan struct{}, 1), release: make(chan struct{})}
	req := httptest.NewRequest("POST", "/telegram/bots/1", body)
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", d.secret)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); d.a.server.Handler.ServeHTTP(response, req) }()
	released := false
	defer func() {
		if !released {
			close(body.release)
		}
		<-done
	}()
	select {
	case <-body.started:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook did not authenticate")
	}
	for _, action := range []string{"disconnect", "reconnect", "activate"} {
		if got := d.b.post("/bots/1/"+action, url.Values{"token": {replacementToken}, "operate": {"yes"}}); got.Code != 303 {
			t.Fatalf("%s: %d", action, got.Code)
		}
	}
	close(body.release)
	released = true
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook did not finish")
	}
	if response.Code != 401 {
		t.Fatal("stale webhook crossed reconnect boundary")
	}
}

func TestBotDisconnectedCredentialsRemainRemovedAfterRestart(t *testing.T) {
	d := newInquiryDriver(t)
	if got := d.b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	// Audit credentials at rest; the public projection intentionally omits them.
	var tokenBytes, secretBytes int
	if err := d.a.db.QueryRow(`SELECT length(b.encrypted_token),length(d.encrypted_secret) FROM bots b JOIN bot_delivery d ON d.bot_id=b.id WHERE b.id=1`).Scan(&tokenBytes, &secretBytes); err != nil || tokenBytes != 0 || secretBytes != 0 {
		t.Fatal("disconnect retained credential material")
	}
	if err := d.a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	d.a = restarted
	d.b.router = restarted.server.Handler
	if page := d.b.send("GET", "/bots/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "توکن ذخیره\u200cشده حذف شده") {
		t.Fatal("restart lost disconnection")
	}
	d.f.mu.Lock()
	d.f.identityID = 987654
	d.f.mu.Unlock()
	if got := d.b.post("/bots/1/reconnect", url.Values{"token": {replacementToken}}); got.Code != 422 {
		t.Fatal("reconnected another identity")
	}
	d.f.mu.Lock()
	d.f.identityID = 0
	d.f.mu.Unlock()
	if got := d.b.post("/bots/1/reconnect", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := d.b.send("GET", "/bots/1/draft", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "نام شما چیست؟") {
		t.Fatal("restart/reconnect lost retained settings")
	}
}

func TestBotLifecycleStorageFailurePreservesDataAndAllowsRetry(t *testing.T) {
	for _, action := range []string{"disconnect", "delete"} {
		t.Run(action, func(t *testing.T) {
			d := newInquiryDriver(t)
			statement := `CREATE TRIGGER reject_lifecycle BEFORE UPDATE OF encrypted_token ON bots BEGIN SELECT RAISE(ABORT,'private storage failure'); END`
			if action == "delete" {
				statement = `CREATE TRIGGER reject_lifecycle BEFORE DELETE ON bots BEGIN SELECT RAISE(ABORT,'private storage failure'); END`
			}
			if _, err := d.a.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if got := d.b.post("/bots/1/"+action, url.Values{"confirm_delete": {"yes"}}); got.Code != 500 || strings.Contains(got.Body.String(), "private storage failure") {
				t.Fatalf("failure: %d", got.Code)
			}
			if page := d.b.send("GET", "/bots/1/draft", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "نام شما چیست؟") {
				t.Fatal("failed lifecycle removed settings")
			}
			if _, err := d.a.db.Exec(`DROP TRIGGER reject_lifecycle`); err != nil {
				t.Fatal(err)
			}
			if got := d.b.post("/bots/1/"+action, url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
				t.Fatal("lifecycle claim blocked retry")
			}
		})
	}
}

func TestBotReplacementPreservesIdentityAndRequiresActivation(t *testing.T) {
	d := newInquiryDriver(t)
	if got := d.b.post("/bots/1/replace-token", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatalf("replace token: %d", got.Code)
	}
	if page := d.b.send("GET", "/bots/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), `data-bot-state="published.inactive"`) || strings.Contains(page.Body.String(), replacementToken) {
		t.Fatal("replacement must retain the Bot, hide credentials and require explicit activation")
	}
	if got := webhook(d.a, d.secret, `{"update_id":900}`); got.Code != 401 {
		t.Fatal("replacement left old delivery accepting updates")
	}
	if page := d.b.send("GET", "/bots/1/draft", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "نام شما چیست؟") {
		t.Fatal("replacement lost configuration")
	}
	if got := d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.f.mu.Lock()
	d.secret = d.f.secret
	d.f.mu.Unlock()
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
}

func TestBotDisconnectCancelsPollingBeforeRemovingCredentials(t *testing.T) {
	a, b, f := pollingFixture(t)
	activatePolling(t, b)
	f.mu.Lock()
	f.holdPoll = true
	f.pollStarted = make(chan struct{}, 4)
	f.pollCancelled = make(chan struct{}, 4)
	started, cancelled := f.pollStarted, f.pollCancelled
	f.mu.Unlock()
	runDeliveryApp(t, a)
	waitPoll(t, started)
	if got := b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disconnect returned before polling cancellation")
	}
	f.mu.Lock()
	before := len(f.offsets)
	f.mu.Unlock()
	time.Sleep(250 * time.Millisecond)
	f.mu.Lock()
	after := len(f.offsets)
	f.mu.Unlock()
	if after != before {
		t.Fatal("polling continued after disconnect")
	}
	if got := b.send("GET", "/account", nil); got.Code != 200 {
		t.Fatal("disconnect affected account")
	}
}

func TestBotDisconnectCancelsSendAndRejectsRacingWebhook(t *testing.T) {
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
	if got := webhook(a, secret, `{"update_id":700,"message":{"from":{"id":77},"chat":{"id":77,"type":"private"},"text":"/start"}}`); got.Code != 200 {
		t.Fatal(got.Code)
	}
	runDeliveryApp(t, a)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("send did not start")
	}
	if got := b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := webhook(a, secret, `{"update_id":701}`); got.Code != 401 {
		t.Fatal("disconnected delivery accepted")
	}
	f.mu.Lock()
	f.holdSend = false
	sent := len(f.sent)
	f.mu.Unlock()
	if sent != 0 {
		t.Fatal("disconnect failed to cancel send")
	}
}

func TestBotDisconnectRemoteCleanupIsBestEffortAndPreservesForeignWebhook(t *testing.T) {
	for _, tc := range []struct {
		name, url string
		status    int
		warning   bool
	}{
		{"owned", "https://piko.example.test/telegram/bots/1", 0, false},
		{"foreign", "https://elsewhere.example.test/private", 0, false},
		{"revoked", "https://piko.example.test/telegram/bots/1", 401, true},
		{"unavailable", "https://piko.example.test/telegram/bots/1", 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newInquiryDriver(t)
			d.f.mu.Lock()
			d.f.webhook = tc.url
			d.f.apiStatus = tc.status
			before := len(d.f.calls)
			d.f.mu.Unlock()
			got := d.b.post("/bots/1/disconnect", url.Values{})
			if got.Code != 303 || strings.Contains(got.Header().Get("Location"), "cleanup=unavailable") != tc.warning {
				t.Fatalf("disconnect: %d %s", got.Code, got.Header().Get("Location"))
			}
			d.f.mu.Lock()
			remote := d.f.webhook
			calls := append([]string(nil), d.f.calls[before:]...)
			d.f.apiStatus = 0
			d.f.mu.Unlock()
			if tc.name == "owned" {
				if remote != "" {
					t.Fatal("owned webhook retained")
				}
			} else if remote != tc.url {
				t.Fatal("changed foreign or inaccessible delivery")
			}
			if tc.name != "owned" {
				for _, call := range calls {
					if call == "deleteWebhook" || call == "setWebhook" {
						t.Fatal("overwrote foreign or unavailable delivery")
					}
				}
			}
			if got := webhook(d.a, d.secret, `{"update_id":900}`); got.Code != 401 {
				t.Fatal("cleanup failure left local ingress open")
			}
			if got := d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 409 {
				t.Fatal("stored credentials retained")
			}
			if got := d.b.post("/bots/1/reconnect", url.Values{"token": {replacementToken}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if tc.name == "foreign" {
				if got := d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 409 {
					t.Fatal("reconnection bypassed conflict confirmation")
				}
			}
		})
	}
}

func TestBotConfirmedDeletionRemovesPikoDataAndPreservesOtherBots(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام خصوصی", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	preview := d.b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	d.f.mu.Lock()
	d.f.identityID = 222222
	d.f.mu.Unlock()
	if got := d.b.post("/bots/connect", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.f.mu.Lock()
	d.f.identityID = 0
	d.f.mu.Unlock()
	if got := d.b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 || got.Header().Get("Location") != "/bots" {
		t.Fatalf("delete: %d", got.Code)
	}
	for _, path := range []string{"/bots/1", "/bots/1/draft", "/bots/1/submissions", "/bots/1/submissions/1", preview} {
		if got := d.b.send("GET", path, nil); got.Code != 404 {
			t.Fatalf("deleted data reachable: %s %d", path, got.Code)
		}
	}
	for _, path := range []string{"/account", "/bots/2"} {
		if got := d.b.send("GET", path, nil); got.Code != 200 {
			t.Fatalf("unrelated data removed: %s", path)
		}
	}
	// Storage audit checks actual removal, including otherwise invisible orphans.
	for _, table := range []string{"bot_drafts", "bot_previews", "bot_publications", "bot_delivery", "bot_updates", "bot_participants", "bot_submissions"} {
		var count int
		if err := d.a.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE bot_id = 1", table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphan data in %s: %d %v", table, count, err)
		}
	}
	if got := webhook(d.a, d.secret, `{"update_id":900}`); got.Code != 401 {
		t.Fatal("deleted Bot accepts delivery")
	}
	if got := d.b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal("Telegram identity no longer reusable after Piko deletion")
	}
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	d.b.router = restarted.server.Handler
	if got := d.b.send("GET", "/bots/1", nil); got.Code != 404 {
		t.Fatal("deleted Bot returned after restart")
	}
}

func TestBotLifecycleCredentialFailurePreservesExistingOperation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		identity        int64
		apiStatus, want int
	}{
		{"wrong identity", 987654, 0, 422}, {"revoked", 0, 401, 422}, {"unavailable", 0, 503, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newInquiryDriver(t)
			d.f.mu.Lock()
			d.f.identityID = tc.identity
			d.f.apiStatus = tc.apiStatus
			d.f.mu.Unlock()
			if got := d.b.post("/bots/1/replace-token", url.Values{"token": {replacementToken}}); got.Code != tc.want || strings.Contains(got.Body.String(), replacementToken) {
				t.Fatalf("rejection: %d", got.Code)
			}
			d.f.mu.Lock()
			d.f.identityID = 0
			d.f.apiStatus = 0
			d.f.mu.Unlock()
			runDeliveryApp(t, d.a)
			d.text("/start", 2)
			d.f.mu.Lock()
			last := d.f.tokens[len(d.f.tokens)-1]
			d.f.mu.Unlock()
			if last != testBotToken {
				t.Fatal("failed verification replaced working credentials")
			}
		})
	}
}

func TestBotLifecycleControlsRequireOwnerCSRFAndPOST(t *testing.T) {
	d := newInquiryDriver(t)
	visitor := newAccountBrowser(t, d.a.server.Handler)
	visitor.send("GET", "/register", nil)
	other := newAccountBrowser(t, d.a.server.Handler)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("lifecycle-other@example.test", "دیگر", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, action := range []string{"replace-token", "disconnect", "reconnect", "delete"} {
		path := "/bots/1/" + action
		values := url.Values{"token": {replacementToken}, "confirm_delete": {"yes"}}
		if got := visitor.post(path, values); got.Code != 303 {
			t.Fatalf("anonymous %s: %d", action, got.Code)
		}
		if got := other.post(path, values); got.Code != 404 {
			t.Fatalf("cross-owner %s: %d", action, got.Code)
		}
		for _, csrf := range []string{"", "invalid"} {
			if got := d.b.send("POST", path, url.Values{"csrf_token": {csrf}, "token": {replacementToken}, "confirm_delete": {"yes"}}); got.Code != 403 {
				t.Fatalf("CSRF %s: %d", action, got.Code)
			}
		}
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			if got := d.b.send(method, path, url.Values{"csrf_token": {d.b.cookie("piko_csrf")}}); got.Code != 405 {
				t.Fatalf("method %s: %d", action, got.Code)
			}
		}
	}
	for _, values := range []url.Values{{}, {"confirm_delete": {"no"}}, {"confirm_delete": {"yes", "yes"}}} {
		if got := d.b.post("/bots/1/delete", values); got.Code != 422 {
			t.Fatal("deletion lacked explicit single confirmation")
		}
	}
	if got := d.b.send("GET", "/bots/1", nil); got.Code != 200 {
		t.Fatal("rejected operations deleted Bot")
	}
}

func TestBotDisconnectRetainsSubmissionsAndReconnectsSameBot(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام محفوظ", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	if got := d.b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1", "/bots", "/dashboard"} {
		if page := d.b.send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), "اتصال قطع شده") {
			t.Fatalf("disconnected status missing: %s", path)
		}
	}
	page := d.b.send("GET", "/bots/1/connection", nil)
	if !strings.Contains(page.Body.String(), `action="/bots/1/reconnect"`) || strings.Contains(page.Body.String(), `action="/bots/1/replace-token"`) {
		t.Fatal("distinct disconnected controls missing")
	}
	if got := webhook(d.a, d.secret, `{"update_id":900}`); got.Code != 401 {
		t.Fatal("disconnected ingress accepted delivery")
	}
	if got := d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 409 {
		t.Fatal("activated removed credentials")
	}
	if page := d.b.send("GET", "/bots/1/submissions/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "نام محفوظ") {
		t.Fatal("disconnect lost Submission")
	}
	if got := d.b.post("/bots/connect", url.Values{"token": {replacementToken}}); got.Code != 409 {
		t.Fatal("disconnected identity became claimable")
	}
	if got := d.b.post("/bots/1/reconnect", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := webhook(d.a, d.secret, `{"update_id":901}`); got.Code != 401 {
		t.Fatal("reconnect skipped explicit activation")
	}
	if got := d.b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.f.mu.Lock()
	d.secret = d.f.secret
	d.f.mu.Unlock()
	d.text("/start", 2)
	d.countSubmissions(1)
}
