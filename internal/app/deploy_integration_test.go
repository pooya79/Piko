package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/testsupport"
)

func TestDeployGuidesConnectionWithoutChangingDraftOrChats(t *testing.T) {
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	before := b.send("GET", "/bots/1/draft", nil).Body.String()
	got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if got.Code != 409 || !strings.Contains(got.Body.String(), `href="/bots/1/connect"`) {
		t.Fatalf("connection guidance: %d %s", got.Code, got.Body.String())
	}
	if after := b.send("GET", "/bots/1/draft", nil).Body.String(); after != before {
		t.Fatal("Deploy changed the Unconnected Draft")
	}
	if page := b.send("GET", "/bots/1/chats/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "گفتگوی اول") {
		t.Fatal("Deploy lost the Builder chat")
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "نسخهٔ منتشرشده: ۰") {
		t.Fatal("missing credentials published the Draft")
	}
}

// Keep all observations at App HTTP and the external Telegram/provider seams.
func deployFixture(t *testing.T, provider http.HandlerFunc) (*App, *accountBrowser, *telegramFake) {
	t.Helper()
	a, b, f := generalDeployFixture(t, provider)
	if got := b.post("/bots/new", url.Values{"name": {"ربات انتشار"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"گفتگوی اول", "گفتگوی دوم"} {
		if got := b.post("/bots/1/chats", url.Values{"title": {title}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	if got := b.post("/bots/1/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, f
}

func generalDeployFixture(t *testing.T, provider http.HandlerFunc) (*App, *accountBrowser, *telegramFake) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	f := &telegramFake{}
	api := httptest.NewServer(f)
	t.Cleanup(api.Close)
	cfg := Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "deploy-test-session-secret-at-least-32", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", BotPublicURL: "https://piko.example.test", LogLevel: "error", ShutdownPeriod: time.Second}
	if provider != nil {
		model := httptest.NewServer(streamingBuilderProvider(provider))
		t.Cleanup(model.Close)
		cfg.Builder = builder.Config{APIKey: "test-server-key", BaseURL: model.URL + "/v1"}
	}
	a, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(api.URL, api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.stopRequests(); a.builder.Wait(); _ = a.db.Close() })
	b := newAccountBrowser(t, a.server.Handler)
	b.send("GET", "/register", nil)
	if got := b.post("/register", registerValues("deploy-owner@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b, f
}

func TestDeployFirstActivationAndCompatibleDeliveryPreservePause(t *testing.T) {
	_, b, f := deployFixture(t, nil)
	deploy := func(version string) {
		t.Helper()
		got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}})
		if got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="`+version+`"`) {
			t.Fatalf("Deploy: %d %s", got.Code, got.Body.String())
		}
	}
	deploy("1")
	deploy("2")
	if got := b.post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	// Saving a new Draft must not update the immutable live version.
	draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	draft.Set("welcome", "پیش\u200cنویس بررسی\u200cشده")
	if got := b.post("/bots/1/draft", draft); got.Code != 303 {
		t.Fatal(got.Code)
	}
	deploy("3")
	page := b.send("GET", "/bots/1", nil).Body.String()
	if !strings.Contains(page, `action="/bots/1/resume"`) || strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("Deploy resumed paused Bot")
	}
	f.mu.Lock()
	calls := append([]string(nil), f.calls...)
	f.mu.Unlock()
	if strings.Count(strings.Join(calls, ","), "setWebhook") != 1 {
		t.Fatal("compatible active delivery was reconfigured", calls)
	}
	if got := b.post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("explicit Resume did not take effect")
	}
}

func TestDeployPublicationSurvivesActivationFailureAndRetriesOnlyActivation(t *testing.T) {
	_, b, f := deployFixture(t, nil)
	f.mu.Lock()
	f.activationFails = true
	f.mu.Unlock()
	got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if got.Code != 503 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) || !strings.Contains(got.Body.String(), "پیش\u200cنویس منتشر شد، اما") || !strings.Contains(got.Body.String(), `href="/bots/1/activate"`) {
		t.Fatal("partial outcome missing", got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") || strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("failed activation claimed live delivery")
	}
	f.mu.Lock()
	f.activationFails = false
	f.mu.Unlock()
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") || !strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("activation retry rebuilt publication or remained inactive")
	}
}

func TestDeployReusesForeignWebhookConfirmation(t *testing.T) {
	_, b, f := deployFixture(t, nil)
	f.mu.Lock()
	f.webhook = "https://foreign.example.test/private-secret"
	f.mu.Unlock()
	got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if got.Code != 409 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) || !strings.Contains(got.Body.String(), `action="/bots/1/activate"`) || strings.Contains(got.Body.String(), "private-secret") {
		t.Fatal("unsafe webhook-conflict outcome", got.Code)
	}
	matches := regexp.MustCompile(`name="conflict" value="([^"]+)"`).FindStringSubmatch(got.Body.String())
	if len(matches) != 2 {
		t.Fatal("missing conflict confirmation")
	}
	// A changed foreign receiver still needs a fresh explicit confirmation.
	f.mu.Lock()
	f.webhook = "https://changed.example.test/secret"
	f.mu.Unlock()
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {matches[1]}}); got.Code != 409 {
		t.Fatal("stale foreign-webhook confirmation accepted", got.Code)
	}
	page := b.send("GET", "/bots/1/activate", nil)
	matches = regexp.MustCompile(`name="conflict" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(matches) != 2 {
		t.Fatal("missing refreshed confirmation")
	}
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {matches[1]}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
}

func TestDeployRequiresOwnerPOSTCSRFAndExplicitOperation(t *testing.T) {
	a, b, f := deployFixture(t, nil)
	path := "/bots/1/deploy"
	if got := b.send("GET", path, nil); got.Code != 405 {
		t.Fatal(got.Code)
	}
	for _, values := range []url.Values{{}, {"csrf_token": {"invalid"}}} {
		if got := b.send("POST", path, values); got.Code != 403 {
			t.Fatal(got.Code)
		}
	}
	if got := b.post(path, url.Values{}); got.Code != 422 {
		t.Fatal("missing operator consent accepted", got.Code)
	}
	stranger := newAccountBrowser(t, a.server.Handler)
	stranger.send("GET", "/login", nil)
	if got := stranger.post(path, url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal("anonymous Deploy accepted", got.Code)
	}
	stranger.send("GET", "/register", nil)
	if got := stranger.post("/register", registerValues("deploy-stranger@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := stranger.post(path, url.Values{"operate": {"yes"}}); got.Code != 404 {
		t.Fatal("foreign owner Deploy accepted", got.Code)
	}
	if got := b.send("POST", path, url.Values{"csrf_token": {b.cookie(auth.CSRFCookie)}, "operate": {"yes"}}); got.Code != 200 {
		t.Fatal("owner Deploy rejected", got.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 1 {
		t.Fatal("rejected requests mutated Telegram")
	}
}

func TestDeployRejectsActiveRunAcrossChatsAndLegacyPublication(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	_, b, _ := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body any
		_ = json.NewDecoder(r.Body).Decode(&body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		builderTextReply(w)
	})
	defer close(release)
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not started")
	}
	for _, path := range []string{"/bots/1", "/bots/1/chats/1", "/bots/1/chats/2"} {
		page := b.send("GET", path, nil).Body.String()
		form := regexp.MustCompile(`(?s)<form method="post" action="/bots/1/deploy">(.*?)</form>`).FindStringSubmatch(page)
		if len(form) != 2 || !strings.Contains(form[1], "disabled") {
			t.Fatal("Deploy not disabled in", path)
		}
	}
	for _, path := range []string{"/bots/1/deploy", "/bots/1/publish"} {
		if got := b.post(path, url.Values{"operate": {"yes"}}); got.Code != 409 {
			t.Fatal("active run allowed publication", path, got.Code)
		}
	}
	release <- struct{}{}
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
		t.Fatal("completed run did not release Deploy", got.Code)
	}
}

func TestDeployAndRunAdmissionSerializeAcrossAppInstances(t *testing.T) {
	for range 6 {
		func() {
			started, release := make(chan struct{}, 1), make(chan struct{})
			a, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				builderTextReply(w)
			})
			defer close(release)
			api := httptest.NewServer(f)
			defer api.Close()
			second, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(api.URL, api.Client()))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { second.stopRequests(); second.builder.Wait(); _ = second.db.Close() }()
			other := newAccountBrowser(t, second.server.Handler)
			other.jar.SetCookies(other.base, b.jar.Cookies(b.base))
			gate := make(chan struct{})
			admission, deployment := make(chan int, 1), make(chan int, 1)
			go func() {
				<-gate
				admission <- other.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست هم\u200cزمان"}}).Code
			}()
			go func() { <-gate; deployment <- b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}).Code }()
			close(gate)
			if code := <-admission; code != 303 {
				t.Fatal("admission failed", code)
			}
			code := <-deployment
			if code != 200 && code != 409 {
				t.Fatal("racing Deploy failed unsafely", code)
			}
			page := b.send("GET", "/bots/1", nil).Body.String()
			expected := "نسخهٔ منتشرشده: ۰"
			if code == 200 {
				expected = "نسخهٔ منتشرشده: ۱"
			}
			if !strings.Contains(page, expected) {
				t.Fatal("run admission raced past publication guard")
			}
			if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 409 {
				t.Fatal("cross-process busy guard bypassed", got.Code)
			}
			release <- struct{}{}
			waitBuilder(t, other, "/bots/1/chats/1", "succeeded")
		}()
	}
}

func TestDeployRetainsPublishedFlowThroughDraftPreviewUndoAndNewVersions(t *testing.T) {
	var calls atomic.Int64
	a, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		} else {
			builderTextReply(w)
		}
	})
	if got := b.postDraft(t, "/bots/1/draft", inquiryDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	f.mu.Lock()
	secret := f.secret
	f.mu.Unlock()
	d := &formDriver{t: t, a: a, b: b, f: f, secret: secret, update: 100}
	runDeliveryApp(t, a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("شرکت\u200cکننده پیشین", 1)
	// A generated candidate, Preview and guarded Undo never publish themselves.
	saveBuilderChange(t, b, "/bots/1/chats/1")
	if got := b.post("/bots/1/preview", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("Draft/Preview/Undo changed live version")
	}
	updated := inquiryDraft()
	updated["question_prompt"][1] = "پرسش نسخه تازه"
	updated.Set("acknowledgement", "رسید نسخه تازه")
	if got := b.postDraft(t, "/bots/1/draft", updated); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, f, d.sent); sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Deploy changed the unfinished Interaction")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	if sent := waitSent(t, f, d.sent); sent[len(sent)-1].Text != "درخواست شما دریافت شد" {
		t.Fatal("old acknowledgement changed")
	}
	if page := b.send("GET", "/bots/1/submissions/1", nil).Body.String(); !strings.Contains(page, "شرکت\u200cکننده پیشین") {
		t.Fatal("Submission incompatible")
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("شرکت\u200cکننده تازه", 1)
	if sent := waitSent(t, f, d.sent); sent[len(sent)-1].Text != "پرسش نسخه تازه" {
		t.Fatal("new Interaction missed deployed version")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if sent := waitSent(t, f, d.sent); sent[len(sent)-1].Text != "رسید نسخه تازه" {
		t.Fatal("new acknowledgement missing")
	}
}

func TestDeployDisconnectedGuidanceAndPausedReactivation(t *testing.T) {
	_, b, f := deployFixture(t, nil)
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 409 || !strings.Contains(got.Body.String(), "ابتدا تلگرام") {
		t.Fatal("missing reconnection guidance", got.Code)
	}
	if got := b.post("/bots/1/reconnect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۲") || !strings.Contains(page, `action="/bots/1/resume"`) {
		t.Fatal("reactivation lost pause")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 2 {
		t.Fatal("inactive delivery was not reactivated")
	}
}

func TestDeployCommitsStableSnapshotBeforeConcurrentRunAndActivationIO(t *testing.T) {
	providerStarted, providerRelease := make(chan struct{}), make(chan struct{})
	_, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(providerStarted)
		select {
		case <-providerRelease:
		case <-r.Context().Done():
			return
		}
		builderTextReply(w)
	})
	defer close(providerRelease)
	inspectionStarted, inspectionRelease := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.holdInspection = true
	f.inspectionStarted = inspectionStarted
	f.releaseInspection = inspectionRelease
	f.mu.Unlock()
	defer close(inspectionRelease)
	deployment := make(chan int, 1)
	go func() { deployment <- b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}).Code }()
	select {
	case <-inspectionStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("activation inspection not reached")
	}
	// The publication transaction has finished; external I/O cannot hold the
	// SQLite lock or silently include a newly admitted run in that snapshot.
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"عملیات پس از انتشار"}}); got.Code != 303 {
		t.Fatal("activation held publication lock", got.Code)
	}
	select {
	case <-providerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not reached")
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("stable snapshot not committed before admission")
	}
	inspectionRelease <- struct{}{}
	if code := <-deployment; code != 200 {
		t.Fatal("admission invalidated committed publication", code)
	}
	providerRelease <- struct{}{}
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
}

func TestDeployInvalidDraftAndPublicationStorageFailureDoNotActivate(t *testing.T) {
	for _, setup := range []struct {
		name, sql string
		code      int
	}{
		{"invalid Draft", `UPDATE bot_drafts SET definition='{"version":999}' WHERE bot_id=1`, 422},
		{"publication failure", `CREATE TRIGGER fail_publish BEFORE INSERT ON bot_publications BEGIN SELECT RAISE(ABORT,'private database detail'); END`, 500},
	} {
		t.Run(setup.name, func(t *testing.T) {
			a, b, f := deployFixture(t, nil)
			if _, err := a.db.Exec(setup.sql); err != nil {
				t.Fatal(err)
			}
			got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}})
			if got.Code != setup.code || strings.Contains(got.Body.String(), "private database detail") {
				t.Fatal("unsafe publication failure", got.Code)
			}
			if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") {
				t.Fatal("failed publication persisted")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if strings.Contains(strings.Join(f.calls, ","), "setWebhook") {
				t.Fatal("failed publication activated Telegram")
			}
		})
	}
}
