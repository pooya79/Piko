package bot_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestDeployGuidesConnectionWithoutChangingDraftOrChats(t *testing.T) {
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	before := b.Send("GET", "/bots/1/draft", nil).Body.String()
	got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if got.Code != 409 || !strings.Contains(got.Body.String(), `href="/bots/1/connect"`) {
		t.Fatalf("connection guidance: %d %s", got.Code, got.Body.String())
	}
	if after := b.Send("GET", "/bots/1/draft", nil).Body.String(); after != before {
		t.Fatal("Deploy changed the Unconnected Draft")
	}
	if page := b.Send("GET", "/bots/1/chats/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "گفتگوی اول") {
		t.Fatal("Deploy lost the Builder chat")
	}
	if page := b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "نسخهٔ منتشرشده: ۰") {
		t.Fatal("missing credentials published the Draft")
	}
}

func TestDeployFirstActivationAndCompatibleDeliveryPreservePause(t *testing.T) {
	_, b, f := fixture.DeployFixture(t, nil)
	deploy := func(version string) {
		t.Helper()
		got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}})
		if got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="`+version+`"`) {
			t.Fatalf("Deploy: %d %s", got.Code, got.Body.String())
		}
	}
	deploy("1")
	deploy("2")
	if got := b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	// Saving a new Draft must not update the immutable live version.
	draft := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	draft.Set("welcome", "پیش\u200cنویس بررسی\u200cشده")
	if got := b.Post("/bots/1/draft", draft); got.Code != 303 {
		t.Fatal(got.Code)
	}
	deploy("3")
	page := b.Send("GET", "/bots/1", nil).Body.String()
	if !strings.Contains(page, `action="/bots/1/resume"`) || strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("Deploy resumed paused Bot")
	}
	f.Mu.Lock()
	calls := append([]string(nil), f.Calls...)
	f.Mu.Unlock()
	if strings.Count(strings.Join(calls, ","), "setWebhook") != 1 {
		t.Fatal("compatible active delivery was reconfigured", calls)
	}
	if got := b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("explicit Resume did not take effect")
	}
}

func TestDeployPublicationSurvivesActivationFailureAndRetriesOnlyActivation(t *testing.T) {
	_, b, f := fixture.DeployFixture(t, nil)
	f.Mu.Lock()
	f.ActivationFails = true
	f.Mu.Unlock()
	got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if got.Code != 503 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) || !strings.Contains(got.Body.String(), "پیش\u200cنویس منتشر شد، اما") || !strings.Contains(got.Body.String(), `href="/bots/1/activate"`) {
		t.Fatal("partial outcome missing", got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") || strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("failed activation claimed live delivery")
	}
	f.Mu.Lock()
	f.ActivationFails = false
	f.Mu.Unlock()
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") || !strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("activation retry rebuilt publication or remained inactive")
	}
}

func TestDeployReusesForeignWebhookConfirmation(t *testing.T) {
	_, b, f := fixture.DeployFixture(t, nil)
	f.Mu.Lock()
	f.Webhook = "https://foreign.example.test/private-secret"
	f.Mu.Unlock()
	got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if got.Code != 409 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) || !strings.Contains(got.Body.String(), `action="/bots/1/activate"`) || strings.Contains(got.Body.String(), "private-secret") {
		t.Fatal("unsafe webhook-conflict outcome", got.Code)
	}
	matches := regexp.MustCompile(`name="conflict" value="([^"]+)"`).FindStringSubmatch(got.Body.String())
	if len(matches) != 2 {
		t.Fatal("missing conflict confirmation")
	}
	// A changed foreign receiver still needs a fresh explicit confirmation.
	f.Mu.Lock()
	f.Webhook = "https://changed.example.test/secret"
	f.Mu.Unlock()
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {matches[1]}}); got.Code != 409 {
		t.Fatal("stale foreign-webhook confirmation accepted", got.Code)
	}
	page := b.Send("GET", "/bots/1/activate", nil)
	matches = regexp.MustCompile(`name="conflict" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(matches) != 2 {
		t.Fatal("missing refreshed confirmation")
	}
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {matches[1]}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
}

func TestDeployRequiresOwnerPOSTCSRFAndExplicitOperation(t *testing.T) {
	a, b, f := fixture.DeployFixture(t, nil)
	path := "/bots/1/deploy"
	if got := b.Send("GET", path, nil); got.Code != 405 {
		t.Fatal(got.Code)
	}
	for _, values := range []url.Values{{}, {"csrf_token": {"invalid"}}} {
		if got := b.Send("POST", path, values); got.Code != 403 {
			t.Fatal(got.Code)
		}
	}
	if got := b.Post(path, url.Values{}); got.Code != 422 {
		t.Fatal("missing operator consent accepted", got.Code)
	}
	stranger := fixture.NewAccountBrowser(t, a.Handler)
	stranger.Send("GET", "/login", nil)
	if got := stranger.Post(path, url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal("anonymous Deploy accepted", got.Code)
	}
	stranger.Send("GET", "/register", nil)
	if got := stranger.Post("/register", fixture.RegisterValues("deploy-stranger@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := stranger.Post(path, url.Values{"operate": {"yes"}}); got.Code != 404 {
		t.Fatal("foreign owner Deploy accepted", got.Code)
	}
	if got := b.Send("POST", path, url.Values{"csrf_token": {b.Cookie(auth.CSRFCookie)}, "operate": {"yes"}}); got.Code != 200 {
		t.Fatal("owner Deploy rejected", got.Code)
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if strings.Count(strings.Join(f.Calls, ","), "setWebhook") != 1 {
		t.Fatal("rejected requests mutated Telegram")
	}
}

func TestDeployRejectsActiveRunAcrossChatsAndLegacyPublication(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	_, b, _ := fixture.DeployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body any
		_ = json.NewDecoder(r.Body).Decode(&body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fixture.BuilderTextReply(w)
	})
	defer close(release)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not started")
	}
	for _, path := range []string{"/bots/1", "/bots/1/chats/1", "/bots/1/chats/2"} {
		page := b.Send("GET", path, nil).Body.String()
		form := regexp.MustCompile(`(?s)<form method="post" action="/bots/1/deploy">(.*?)</form>`).FindStringSubmatch(page)
		if len(form) != 2 || !strings.Contains(form[1], "disabled") {
			t.Fatal("Deploy not disabled in", path)
		}
	}
	for _, path := range []string{"/bots/1/deploy", "/bots/1/publish"} {
		if got := b.Post(path, url.Values{"operate": {"yes"}}); got.Code != 409 {
			t.Fatal("active run allowed publication", path, got.Code)
		}
	}
	release <- struct{}{}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
		t.Fatal("completed run did not release Deploy", got.Code)
	}
}

func TestDeployAndRunAdmissionSerializeAcrossAppInstances(t *testing.T) {
	for range 6 {
		func() {
			started, release := make(chan struct{}, 1), make(chan struct{})
			a, b, f := fixture.DeployFixture(t, func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				fixture.BuilderTextReply(w)
			})
			defer close(release)
			api := httptest.NewServer(f)
			defer api.Close()
			second, err := fixture.NewWithTelegram(t.Context(), a.Config, telegram.NewClient(api.URL, api.Client()))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { second.StopWork(); second.Builder.Wait(); _ = second.DB.Close() }()
			other := fixture.NewAccountBrowser(t, second.Handler)
			other.Jar.SetCookies(other.Base, b.Jar.Cookies(b.Base))
			gate := make(chan struct{})
			admission, deployment := make(chan int, 1), make(chan int, 1)
			go func() {
				<-gate
				admission <- other.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست هم\u200cزمان"}}).Code
			}()
			go func() { <-gate; deployment <- b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}).Code }()
			close(gate)
			if code := <-admission; code != 303 {
				t.Fatal("admission failed", code)
			}
			code := <-deployment
			if code != 200 && code != 409 {
				t.Fatal("racing Deploy failed unsafely", code)
			}
			page := b.Send("GET", "/bots/1", nil).Body.String()
			expected := "نسخهٔ منتشرشده: ۰"
			if code == 200 {
				expected = "نسخهٔ منتشرشده: ۱"
			}
			if !strings.Contains(page, expected) {
				t.Fatal("run admission raced past publication guard")
			}
			if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 409 {
				t.Fatal("cross-process busy guard bypassed", got.Code)
			}
			release <- struct{}{}
			fixture.WaitBuilder(t, other, "/bots/1/chats/1", "succeeded")
		}()
	}
}

func TestDeployDisconnectedGuidanceAndPausedReactivation(t *testing.T) {
	_, b, f := fixture.DeployFixture(t, nil)
	if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 409 || !strings.Contains(got.Body.String(), "ابتدا تلگرام") {
		t.Fatal("missing reconnection guidance", got.Code)
	}
	if got := b.Post("/bots/1/reconnect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۲") || !strings.Contains(page, `action="/bots/1/resume"`) {
		t.Fatal("reactivation lost pause")
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if strings.Count(strings.Join(f.Calls, ","), "setWebhook") != 2 {
		t.Fatal("inactive delivery was not reactivated")
	}
}

func TestDeployCommitsStableSnapshotBeforeConcurrentRunAndActivationIO(t *testing.T) {
	providerStarted, providerRelease := make(chan struct{}), make(chan struct{})
	_, b, f := fixture.DeployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(providerStarted)
		select {
		case <-providerRelease:
		case <-r.Context().Done():
			return
		}
		fixture.BuilderTextReply(w)
	})
	defer close(providerRelease)
	inspectionStarted, inspectionRelease := make(chan struct{}), make(chan struct{})
	f.Mu.Lock()
	f.HoldInspection = true
	f.InspectionStarted = inspectionStarted
	f.ReleaseInspection = inspectionRelease
	f.Mu.Unlock()
	defer close(inspectionRelease)
	deployment := make(chan int, 1)
	go func() { deployment <- b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}).Code }()
	select {
	case <-inspectionStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("activation inspection not reached")
	}
	// The publication transaction has finished; external I/O cannot hold the
	// SQLite lock or silently include a newly admitted run in that snapshot.
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"عملیات پس از انتشار"}}); got.Code != 303 {
		t.Fatal("activation held publication lock", got.Code)
	}
	select {
	case <-providerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not reached")
	}
	if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("stable snapshot not committed before admission")
	}
	inspectionRelease <- struct{}{}
	if code := <-deployment; code != 200 {
		t.Fatal("admission invalidated committed publication", code)
	}
	providerRelease <- struct{}{}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
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
			a, b, f := fixture.DeployFixture(t, nil)
			if _, err := a.DB.Exec(setup.sql); err != nil {
				t.Fatal(err)
			}
			got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}})
			if got.Code != setup.code || strings.Contains(got.Body.String(), "private database detail") {
				t.Fatal("unsafe publication failure", got.Code)
			}
			if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") {
				t.Fatal("failed publication persisted")
			}
			f.Mu.Lock()
			defer f.Mu.Unlock()
			if strings.Contains(strings.Join(f.Calls, ","), "setWebhook") {
				t.Fatal("failed publication activated Telegram")
			}
		})
	}
}
