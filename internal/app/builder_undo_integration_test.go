package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/builder"
)

func undoFixture(t *testing.T) (*App, *accountBrowser) {
	t.Helper()
	var calls atomic.Int64
	return builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		} else {
			builderTextReply(w)
		}
	})
}

func TestBuilderUndoRefusesInterveningManualAndOtherChatEdits(t *testing.T) {
	for _, edit := range []string{"identical manual save", "away and back", "another chat"} {
		t.Run(edit, func(t *testing.T) {
			_, b := undoFixture(t)
			saveBuilderChange(t, b, "/bots/1/chats/1")
			if edit == "another chat" {
				saveBuilderChange(t, b, "/bots/1/chats/2")
			} else {
				v := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
				if edit == "away and back" {
					v.Set("welcome", "ویرایش میان\u200cراه")
				}
				if got := b.post("/bots/1/draft", v); got.Code != 303 {
					t.Fatal(got.Code)
				}
				if edit == "away and back" {
					v.Set("welcome", "Hello")
					v.Set("draft_revision", "3")
					if got := b.post("/bots/1/draft", v); got.Code != 303 {
						t.Fatal(got.Code)
					}
				}
			}
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			path := "/bots/1/chats/1/runs/1/undo"
			if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); strings.Contains(page, `action="`+path+`"`) || !strings.Contains(page, "دیگر در دسترس نیست") {
				t.Fatal("stale Undo is not visibly unavailable")
			}
			if got := b.post(path, url.Values{}); got.Code != 409 || !strings.Contains(got.Body.String(), "پیش\u200cنویس فعلی حفظ شد") {
				t.Fatal("stale Undo was not honestly refused", got.Code)
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
				t.Fatal("Undo erased newer work")
			}
			if edit == "another chat" {
				if got := b.post("/bots/1/chats/2/runs/2/undo", url.Values{}); got.Code != 303 {
					t.Fatal("latest other-chat change cannot be undone", got.Code)
				}
				if got := b.post(path, url.Values{}); got.Code != 409 {
					t.Fatal("Undo reopened arbitrary historical rollback")
				}
			}
		})
	}
}

func TestBuilderUndoRequiresOwnerPOSTCSRFAndMatchingRun(t *testing.T) {
	a, b := undoFixture(t)
	saveBuilderChange(t, b, "/bots/1/chats/1")
	path := "/bots/1/chats/1/runs/1/undo"
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	for _, method := range []string{"GET", "PUT"} {
		if got := b.send(method, path, url.Values{"csrf_token": {b.cookie(auth.CSRFCookie)}}); got.Code != 405 {
			t.Fatalf("%s Undo: %d", method, got.Code)
		}
	}
	if got := b.send("DELETE", path, url.Values{"csrf_token": {b.cookie(auth.CSRFCookie)}}); got.Code != 403 {
		t.Fatal("DELETE bypassed mutation protection", got.Code)
	}
	for _, values := range []url.Values{{}, {"csrf_token": {"wrong"}}} {
		if got := b.send("POST", path, values); got.Code != 403 {
			t.Fatal("Undo bypassed CSRF", got.Code)
		}
	}
	stranger := newAccountBrowser(t, a.server.Handler)
	stranger.send("GET", "/login", nil)
	if got := stranger.post(path, url.Values{}); got.Code != 303 {
		t.Fatal("anonymous Undo allowed", got.Code)
	}
	stranger.send("GET", "/register", nil)
	if got := stranger.post("/register", registerValues("undo-stranger@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := stranger.post(path, url.Values{}); got.Code != 404 || strings.Contains(got.Body.String(), "Hello") {
		t.Fatal("another owner accessed Undo", got.Code)
	}
	for _, path := range []string{"/bots/1/chats/2/runs/1/undo", "/bots/1/chats/1/runs/999/undo", "/bots/1/chats/1/runs/0/undo", "/bots/1/chats/1/runs/nope/undo", "/bots/2/chats/1/runs/1/undo"} {
		if got := b.post(path, url.Values{}); got.Code != 404 {
			t.Fatalf("mismatched Undo %s: %d", path, got.Code)
		}
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected requests changed Draft")
	}
}

func TestBuilderUndoSurvivesRestartWithoutProviderCredentials(t *testing.T) {
	a, b := undoFixture(t)
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	saveBuilderChange(t, b, "/bots/1/chats/1")
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	cfg := a.cfg
	cfg.Builder.APIKey = ""
	restarted, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	path := "/bots/1/chats/1/runs/1/undo"
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `action="`+path+`"`) {
		t.Fatal("restart or absent provider lost Undo")
	}
	if got := b.post(path, url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before.Set("draft_revision", "3")
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("restart lost preceding Draft snapshot")
	}
	restarted.stopRequests()
	restarted.builder.Wait()
	_ = restarted.db.Close()
	second, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.stopRequests(); second.builder.Wait(); _ = second.db.Close() })
	b.router = second.server.Handler
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-run-result="undone"`) || !strings.Contains(page, "بازگردانده شد") {
		t.Fatal("restart lost successful Undo outcome")
	}
	if got := b.post(path, url.Values{}); got.Code != 409 {
		t.Fatal("restart permitted repeated Undo")
	}
}

func TestBuilderUndoRacesAcrossAppInstancesOnlyRestoreOnce(t *testing.T) {
	a, b := undoFixture(t)
	saveBuilderChange(t, b, "/bots/1/chats/1")
	second, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.stopRequests(); second.builder.Wait(); _ = second.db.Close() })
	other := newAccountBrowser(t, second.server.Handler)
	other.jar = b.jar
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, browser := range []*accountBrowser{b, other} {
		wg.Go(func() {
			<-start
			codes <- browser.post("/bots/1/chats/1/runs/1/undo", url.Values{}).Code
		})
	}
	close(start)
	wg.Wait()
	first, last := <-codes, <-codes
	if first+last != 303+409 || (first != 303 && first != 409) {
		t.Fatalf("Undo race: %d %d", first, last)
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); after.Get("draft_revision") != "3" {
		t.Fatal("racing Undo advanced Draft more than once")
	}
}

func TestBuilderUndoRacesWithManualSaveWithoutLosingNewerWork(t *testing.T) {
	a, b := undoFixture(t)
	saveBuilderChange(t, b, "/bots/1/chats/1")
	manual := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	manual.Set("welcome", "کار جدید مالک")
	other := newAccountBrowser(t, a.server.Handler)
	other.jar = b.jar
	start := make(chan struct{})
	undoCode, manualCode := make(chan int, 1), make(chan int, 1)
	var wg sync.WaitGroup
	wg.Go(func() { <-start; undoCode <- b.post("/bots/1/chats/1/runs/1/undo", url.Values{}).Code })
	wg.Go(func() { <-start; manualCode <- other.post("/bots/1/draft", manual).Code })
	close(start)
	wg.Wait()
	u, m := <-undoCode, <-manualCode
	if !((u == 303 && m == 409) || (u == 409 && m == 303)) {
		t.Fatalf("manual/Undo race: %d %d", u, m)
	}
	after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if after.Get("draft_revision") != "3" || (m == 303 && after.Get("welcome") != "کار جدید مالک") {
		t.Fatal("Undo race lost a committed manual change")
	}
}

func TestBuilderUndoInvalidatesPendingBuilderResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n%2 == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
			return
		}
		if n == 4 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		builderTextReply(w)
	})
	defer close(release)
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	saveBuilderChange(t, b, "/bots/1/chats/1")
	if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"ویرایش بعدی"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("pending Builder run did not stage its change")
	}
	if got := b.post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	release <- struct{}{}
	page := waitBuilder(t, b, "/bots/1/chats/2", "failed")
	if !strings.Contains(page, `data-run-result="conflict"`) || strings.Contains(page, "/undo\"") {
		t.Fatal("pending result ignored Undo's fresh revision")
	}
	before.Set("draft_revision", "3")
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("pending Builder overwrote Undo")
	}
}

func TestBuilderUndoUnavailableForTurnsWithoutSuccessfulChange(t *testing.T) {
	for _, kind := range []string{"conversation", "invalid", "provider failure", "stopped", "interrupted"} {
		t.Run(kind, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			var calls atomic.Int64
			a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 && kind != "conversation" && kind != "provider failure" {
					candidate := structuredDraft
					if kind == "invalid" {
						candidate = `{}`
					}
					builderToolReply(w, "prepare_draft", map[string]string{"definition": candidate})
					return
				}
				if kind == "stopped" || kind == "interrupted" {
					close(started)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				if kind == "provider failure" {
					w.WriteHeader(500)
					return
				}
				builderTextReply(w)
			})
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			status := "failed"
			switch kind {
			case "conversation":
				status = "succeeded"
			case "stopped", "interrupted":
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("provider did not start")
				}
				if kind == "stopped" {
					if got := b.post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
						t.Fatal(got.Code)
					}
					status = "stopped"
				} else {
					a.stopRequests()
					a.builder.Wait()
					_ = a.db.Close()
					restarted, err := New(t.Context(), a.cfg)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
					b.router = restarted.server.Handler
					status = "interrupted"
				}
			}
			page := waitBuilder(t, b, "/bots/1/chats/1", status)
			if strings.Contains(page, "/undo\"") {
				t.Fatal("turn without a saved change offers Undo")
			}
			if got := b.post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 409 {
				t.Fatal("turn without a saved change allowed Undo", got.Code)
			}
			if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
				t.Fatal("unavailable Undo changed Draft")
			}
		})
	}
}

func TestBuilderUndoStorageFailureRollsBackAndRemainsAvailable(t *testing.T) {
	a, b := undoFixture(t)
	saveBuilderChange(t, b, "/bots/1/chats/1")
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if _, err := a.db.Exec(`CREATE TRIGGER fail_undo_feedback BEFORE INSERT ON builder_messages WHEN NEW.content='builder.run.undone' BEGIN SELECT RAISE(ABORT,'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	path := "/bots/1/chats/1/runs/1/undo"
	if got := b.post(path, url.Values{}); got.Code != 500 {
		t.Fatal("failed Undo was reported successful", got.Code)
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("failed feedback left a partial restoration")
	}
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `action="`+path+`"`) || strings.Contains(page, `data-run-result="undone"`) {
		t.Fatal("failed Undo consumed the saved outcome")
	}
	if _, err := a.db.Exec("DROP TRIGGER fail_undo_feedback"); err != nil {
		t.Fatal(err)
	}
	if got := b.post(path, url.Values{}); got.Code != 303 {
		t.Fatal("retry after storage recovery failed", got.Code)
	}
}

func TestBuilderUndoLeavesLivePublicationInteractionsAndSubmissionsIntact(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام ثبت\u200cشده", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("تعامل فعال", 1)
	before := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String())
	submission := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String()
	var calls atomic.Int64
	fake := httptest.NewServer(streamingBuilderProvider(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": structuredDraft})
		} else {
			builderTextReply(w)
		}
	}))
	t.Cleanup(fake.Close)
	if err := d.a.builder.Configure(builder.Config{APIKey: "test-server-key", BaseURL: fake.URL + "/v1"}, time.Now); err != nil {
		t.Fatal(err)
	}
	if got := d.b.post("/bots/1/chats", url.Values{"title": {"ویرایش پیش\u200cنویس"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	saveBuilderChange(t, d.b, "/bots/1/chats/1")
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	botPage := d.b.send("GET", "/bots/1", nil).Body.String()
	if got := d.b.post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before.Set("draft_revision", "4")
	if after := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("Undo failed to restore Form configuration")
	}
	if page := d.b.send("GET", "/bots/1", nil).Body.String(); page != botPage {
		t.Fatal("Undo changed publication/delivery/pause")
	}
	if page := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String(); page != submission {
		t.Fatal("Undo changed a completed Submission")
	}
	if got := d.b.post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Undo changed a version-pinned Interaction")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	d.text("/start", 2)
	sent = waitSent(t, d.f, d.sent)
	if sent[len(sent)-2].Text != "Hello" {
		t.Fatal("Undo reverted the newly published Flow for fresh Interactions")
	}
}

func saveBuilderChange(t *testing.T, b *accountBrowser, chat string) string {
	t.Helper()
	if got := b.post(chat+"/messages", url.Values{"message": {"منوی ساعت کار بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return waitBuilder(t, b, chat, "succeeded")
}

func TestBuilderUndoRestoresDraftAtFreshRevisionOnce(t *testing.T) {
	_, b := undoFixture(t)
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	page := saveBuilderChange(t, b, "/bots/1/chats/1")
	path := "/bots/1/chats/1/runs/1/undo"
	if !strings.Contains(page, `action="`+path+`"`) {
		t.Fatal("successful change offers no Undo")
	}
	// A late Stop must leave the applied change and its Undo available.
	if got := b.post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post(path, url.Values{}); got.Code != 303 {
		t.Fatalf("Undo: %d", got.Code)
	}
	before.Set("draft_revision", "3")
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("Undo did not restore the preceding Draft with a fresh revision")
	}
	page = b.send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(page, `data-run-result="undone"`) || strings.Contains(page, `action="`+path+`"`) || !strings.Contains(page, "بازگردانده شد") {
		t.Fatal("Undo outcome is not honest or durable")
	}
	if got := b.post(path, url.Values{}); got.Code != 409 {
		t.Fatalf("repeated Undo: %d", got.Code)
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("repeated Undo mutated the Draft")
	}
}
