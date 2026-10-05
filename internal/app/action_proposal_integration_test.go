package app

import (
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
)

func proposedAction(t *testing.T, b *accountBrowser, action string) string {
	t.Helper()
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {action}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	matches := regexp.MustCompile(`data-action-proposal="([0-9]+)"`).FindAllStringSubmatch(page, -1)
	if len(matches) == 0 {
		t.Fatal("concrete action card missing")
	}
	return "/bots/1/proposals/" + matches[len(matches)-1][1] + "/confirm"
}

func TestConversationalProposalIsDisabledWhileAnotherChatWorks(t *testing.T) {
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	_, b, _ := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			builderToolReply(w, "propose_action", map[string]string{"action": "deploy"})
		case 2:
			builderTextReply(w)
		default:
			close(started)
			select {
			case <-release:
				builderTextReply(w)
			case <-r.Context().Done():
			}
		}
	})
	defer close(release)
	target := proposedAction(t, b, "منتشر کن")
	if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"پرسش"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("run not started")
	}
	page := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	if strings.Contains(page, `action="`+target+`"`) || !strings.Contains(page, "سازنده هنوز") {
		t.Fatal("busy card actionable")
	}
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 409 {
		t.Fatal("busy action executed", got.Code)
	}
	release <- struct{}{}
	waitBuilder(t, b, "/bots/1/chats/2", "succeeded")
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal("busy rejection consumed confirmation", got.Code)
	}
}

func TestConversationalConcurrentConfirmationsAcrossServersExecuteOnce(t *testing.T) {
	a, b, f := deployFixture(t, actionProvider("deploy"))
	target := proposedAction(t, b, "منتشر کن")
	api := httptest.NewServer(f)
	defer api.Close()
	second, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(api.URL, api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { second.stopRequests(); second.builder.Wait(); _ = second.db.Close() }()
	other := newAccountBrowser(t, second.server.Handler)
	other.jar.SetCookies(other.base, b.jar.Cookies(b.base))
	started, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.holdInspection = true
	f.inspectionStarted = started
	f.releaseInspection = release
	f.mu.Unlock()
	defer close(release)
	done := make(chan int, 1)
	go func() { done <- b.post(target, url.Values{"operate": {"yes"}}).Code }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("activation not reached")
	}
	if got := other.post(target, url.Values{"operate": {"yes"}}); got.Code != 409 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
		t.Fatal("concurrent confirmation did not show uncertain outcome", got.Code)
	}
	release <- struct{}{}
	if code := <-done; code != 200 {
		t.Fatal(code)
	}
	if got := other.post(target, url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if page := other.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("concurrent confirmations republished")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 1 {
		t.Fatal("concurrent confirmations reactivated")
	}
}

func TestConversationalFailedOrDestructiveProposalsNeverBecomeActionable(t *testing.T) {
	for _, scenario := range []string{"destructive", "failed reply", "two actions", "draft and action", "storage failure"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int64
			a, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				switch {
				case scenario == "destructive":
					builderToolReply(w, "propose_action", map[string]string{"action": "delete"})
				case n == 1:
					builderToolReply(w, "propose_action", map[string]string{"action": "deploy"})
				case scenario == "failed reply":
					w.WriteHeader(500)
				case scenario == "two actions" && n == 2:
					builderToolReply(w, "propose_action", map[string]string{"action": "pause"})
				case scenario == "draft and action" && n == 2:
					builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
				default:
					builderTextReply(w)
				}
			})
			if scenario == "storage failure" {
				if _, err := a.db.Exec(`CREATE TRIGGER reject_proposal BEFORE INSERT ON bot_action_proposals BEGIN SELECT RAISE(ABORT,'private failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
			b.post("/bots/1/chats/1/messages", url.Values{"message": {"عملیات"}})
			page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
			if strings.Contains(page, `data-action-proposal=`) || strings.Contains(page, "private failure") {
				t.Fatal("failed or unsupported action persisted")
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
				t.Fatal("failed operational turn changed Draft")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if strings.Contains(strings.Join(f.calls, ","), "setWebhook") {
				t.Fatal("model executed a live operation")
			}
		})
	}
}

func TestConversationalProposalRejectsInterveningDraftBeforeCompletion(t *testing.T) {
	var calls atomic.Int64
	staged, release := make(chan struct{}), make(chan struct{})
	_, b, _ := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			builderToolReply(w, "propose_action", map[string]string{"action": "deploy"})
			return
		}
		close(staged)
		select {
		case <-release:
			builderTextReply(w)
		case <-r.Context().Done():
		}
	})
	defer close(release)
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"منتشر کن"}})
	select {
	case <-staged:
	case <-time.After(3 * time.Second):
		t.Fatal("proposal not staged")
	}
	draft := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.post("/bots/1/draft", draft); got.Code != 303 {
		t.Fatal(got.Code)
	}
	release <- struct{}{}
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if strings.Contains(page, `data-action-proposal=`) || !strings.Contains(page, `data-run-result="conflict"`) {
		t.Fatal("stale prepared card persisted")
	}
}

func TestConversationalPublicationFailureRollsBackConfirmationClaim(t *testing.T) {
	a, b, f := deployFixture(t, actionProvider("deploy"))
	target := proposedAction(t, b, "منتشر کن")
	if _, err := a.db.Exec(`CREATE TRIGGER reject_action_publish BEFORE INSERT ON bot_publications BEGIN SELECT RAISE(ABORT,'private failure'); END`); err != nil {
		t.Fatal(err)
	}
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 500 || strings.Contains(got.Body.String(), "private failure") {
		t.Fatal("publication error unsafe", got.Code)
	}
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `action="`+target+`"`) {
		t.Fatal("rolled back claim consumed card")
	}
	if _, err := a.db.Exec(`DROP TRIGGER reject_action_publish`); err != nil {
		t.Fatal(err)
	}
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
		t.Fatal("retry after publication rollback failed", got.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 1 {
		t.Fatal("failed publication activated")
	}
}

func TestConversationalCardsExplainUnavailableOperations(t *testing.T) {
	for _, state := range []string{"disconnected", "inactive pause", "inactive resume"} {
		t.Run(state, func(t *testing.T) {
			action := "deploy"
			if strings.Contains(state, "pause") {
				action = "pause"
			}
			if strings.Contains(state, "resume") {
				action = "resume"
			}
			_, b, _ := deployFixture(t, actionProvider(action))
			if state == "disconnected" {
				if got := b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			}
			target := proposedAction(t, b, action)
			page := b.send("GET", "/bots/1/chats/1", nil).Body.String()
			if strings.Contains(page, `action="`+target+`"`) {
				t.Fatal("unavailable operation is actionable")
			}
			if state == "disconnected" && !strings.Contains(page, `href="/bots/1"`) {
				t.Fatal("missing reconnection guidance")
			}
			if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 409 {
				t.Fatal("unavailable action executed", got.Code)
			}
		})
	}
}

func TestConversationalUnconnectedDeployGuidesCredentialScreen(t *testing.T) {
	_, b := builderFixture(t, builder.Config{}, actionProvider("deploy"))
	target := proposedAction(t, b, "منتشر کن")
	page := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	if strings.Contains(page, `action="`+target+`"`) || !strings.Contains(page, `href="/bots/1/connect"`) {
		t.Fatal("missing dedicated connection guidance")
	}
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 409 {
		t.Fatal("Unconnected deployment executed", got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") {
		t.Fatal("unconnected proposal published")
	}
}

func TestConversationalDeployRetainsPartialOutcomeAcrossRestartWithoutRepublishing(t *testing.T) {
	a, b, f := deployFixture(t, actionProvider("deploy"))
	target := proposedAction(t, b, "منتشر کن")
	f.mu.Lock()
	f.activationFails = true
	f.mu.Unlock()
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) || !strings.Contains(got.Body.String(), `href="/bots/1/activate"`) {
		t.Fatal("partial deployment lost", got.Code)
	}
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	api := httptest.NewServer(f)
	defer api.Close()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(api.URL, api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() }()
	b.router = restarted.server.Handler
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
		t.Fatal("restart replay did not retain result", got.Code)
	}
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, "پیش\u200cنویس منتشر شد، اما") || strings.Contains(page, `action="`+target+`"`) {
		t.Fatal("partial card not durable")
	}
	f.mu.Lock()
	f.activationFails = false
	f.mu.Unlock()
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal("activation retry failed", got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("activation retry republished")
	}
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, "وضعیت فعلی ربات:") || strings.Contains(page, `href="/bots/1/activate"`) {
		t.Fatal("recovered activation card still offers unnecessary retry")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 2 {
		t.Fatal("duplicate/restart replay repeated activation")
	}
}

func TestConversationalActionConfirmationRejectsOtherOwnersAndCSRF(t *testing.T) {
	a, b, _ := deployFixture(t, actionProvider("deploy"))
	target := proposedAction(t, b, "منتشر کن")
	for _, invalid := range []string{"bad", "0", "999"} {
		if got := b.post("/bots/1/proposals/"+invalid+"/confirm", url.Values{"operate": {"yes"}}); got.Code != 404 {
			t.Fatal("invalid proposal accepted", got.Code)
		}
	}
	if got := b.send("GET", target, nil); got.Code != 405 {
		t.Fatal("GET executes action", got.Code)
	}
	for _, values := range []url.Values{{"operate": {"yes"}}, {"operate": {"yes"}, "csrf_token": {"wrong"}}} {
		if got := b.send("POST", target, values); got.Code != 403 {
			t.Fatal("CSRF accepted", got.Code)
		}
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("proposal-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{target, "/bots/1/chats/1"} {
		method := "POST"
		if strings.Contains(path, "chats") {
			method = "GET"
		}
		if got := other.send(method, path, url.Values{"csrf_token": {other.cookie(auth.CSRFCookie)}, "operate": {"yes"}}); got.Code != 404 {
			t.Fatal("cross-owner proposal access", got.Code)
		}
	}
	if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal("owner rejected", got.Code)
	}
}

func TestConversationalDeployRequiresExplicitConsentAndIsDuplicateSafe(t *testing.T) {
	var calls atomic.Int64
	_, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			builderToolReply(w, "propose_action", map[string]string{"action": "deploy"})
		} else {
			builderTextReply(w)
		}
	})
	target := proposedAction(t, b, "منتشر کن")
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۰") {
		t.Fatal("model executed deployment")
	}
	if got := b.post(target, url.Values{}); got.Code != 422 {
		t.Fatal("missing operating consent accepted", got.Code)
	}
	for range 2 {
		got := b.post(target, url.Values{"operate": {"yes"}})
		if got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
			t.Fatal("confirmation outcome", got.Code, got.Body.String())
		}
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
		t.Fatal("duplicate confirmation republished")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 1 {
		t.Fatal("duplicate confirmation reactivated")
	}
}

func actionProvider(action string) http.HandlerFunc {
	var calls atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			builderToolReply(w, "propose_action", map[string]string{"action": action})
		} else {
			builderTextReply(w)
		}
	}
}

func TestConversationalActionsRejectChangedStateIncludingPauseABA(t *testing.T) {
	for _, change := range []string{"draft", "publication", "credentials", "disconnect", "pause ABA", "rename"} {
		t.Run(change, func(t *testing.T) {
			_, b, _ := deployFixture(t, actionProvider("deploy"))
			if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
				t.Fatal(got.Code)
			}
			target := proposedAction(t, b, "منتشر کن")
			var gotCode int
			switch change {
			case "draft":
				gotCode = b.post("/bots/1/draft", renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())).Code
			case "publication":
				gotCode = b.post("/bots/1/publish", url.Values{}).Code
			case "credentials":
				gotCode = b.post("/bots/1/replace-token", url.Values{"token": {testBotToken}}).Code
			case "disconnect":
				gotCode = b.post("/bots/1/disconnect", url.Values{}).Code
			case "rename":
				gotCode = b.post("/bots/1/name", url.Values{"name": {"نام تازه"}}).Code
			case "pause ABA":
				if got := b.post("/bots/1/pause", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
				gotCode = b.post("/bots/1/resume", url.Values{}).Code
			}
			if gotCode != 303 {
				t.Fatal("state change failed", gotCode)
			}
			if got := b.post(target, url.Values{"operate": {"yes"}}); got.Code != 409 || !strings.Contains(got.Body.String(), "پیشنهاد تازه") {
				t.Fatal("stale proposal executed", got.Code)
			}
			if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); strings.Contains(page, `action="`+target+`"`) {
				t.Fatal("stale card still actionable")
			}
		})
	}
}

func TestConversationalPauseResumeAndDeployPreservePauseChoice(t *testing.T) {
	var calls atomic.Int64
	actions := []string{"pause", "deploy", "resume"}
	_, b, f := deployFixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n%2 == 1 {
			builderToolReply(w, "propose_action", map[string]string{"action": actions[(n-1)/2]})
		} else {
			builderTextReply(w)
		}
	})
	if got := b.post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	pause := proposedAction(t, b, "متوقف کن")
	for range 2 {
		if got := b.post(pause, url.Values{}); got.Code != 200 || !strings.Contains(got.Body.String(), "فعالیت ربات متوقف شد") {
			t.Fatal("Pause failed", got.Code)
		}
	}
	deploy := proposedAction(t, b, "منتشر کن")
	if got := b.post(deploy, url.Values{"operate": {"yes"}}); got.Code != 200 || !strings.Contains(got.Body.String(), "توقف ربات حفظ شد") {
		t.Fatal("Deploy lost pause", got.Code, got.Body.String())
	}
	resume := proposedAction(t, b, "ادامه بده")
	if got := b.post(resume, url.Values{}); got.Code != 200 || !strings.Contains(got.Body.String(), "فعالیت ربات ادامه یافت") {
		t.Fatal("Resume failed", got.Code)
	}
	// Replaying the earlier Pause after Resume must never pause again.
	if got := b.post(pause, url.Values{}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, `action="/bots/1/pause"`) {
		t.Fatal("old Pause replay changed state")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Count(strings.Join(f.calls, ","), "setWebhook") != 1 {
		t.Fatal("compatible delivery was reconfigured")
	}
}
