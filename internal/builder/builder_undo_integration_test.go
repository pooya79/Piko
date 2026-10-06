package builder_test

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderUndoRefusesInterveningManualAndOtherChatEdits(t *testing.T) {
	for _, edit := range []string{"identical manual save", "away and back", "another chat"} {
		t.Run(edit, func(t *testing.T) {
			_, b := fixture.UndoFixture(t)
			fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
			if edit == "another chat" {
				fixture.SaveBuilderChange(t, b, "/bots/1/chats/2")
			} else {
				v := b.LoadDraft(t, 1)
				if edit == "away and back" {
					v.Set("welcome", "ویرایش میان\u200cراه")
				}
				if err := b.SaveDraft(t, 1, v); err != nil {
					t.Fatal(err)
				}
				if edit == "away and back" {
					v.Set("welcome", "Hello")
					v.Set("draft_revision", "3")
					if err := b.SaveDraft(t, 1, v); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := b.LoadDraft(t, 1)
			path := "/bots/1/chats/1/runs/1/undo"
			if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); strings.Contains(page, `action="`+path+`"`) || !strings.Contains(page, "دیگر در دسترس نیست") {
				t.Fatal("stale Undo is not visibly unavailable")
			}
			if got := b.Post(path, url.Values{}); got.Code != 409 || !strings.Contains(got.Body.String(), "پیش\u200cنویس فعلی حفظ شد") {
				t.Fatal("stale Undo was not honestly refused", got.Code)
			}
			if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
				t.Fatal("Undo erased newer work")
			}
			if edit == "another chat" {
				if got := b.Post("/bots/1/chats/2/runs/2/undo", url.Values{}); got.Code != 303 {
					t.Fatal("latest other-chat change cannot be undone", got.Code)
				}
				if got := b.Post(path, url.Values{}); got.Code != 409 {
					t.Fatal("Undo reopened arbitrary historical rollback")
				}
			}
		})
	}
}

func TestBuilderUndoRequiresOwnerPOSTCSRFAndMatchingRun(t *testing.T) {
	a, b := fixture.UndoFixture(t)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	path := "/bots/1/chats/1/runs/1/undo"
	before := b.LoadDraft(t, 1)
	for _, method := range []string{"GET", "PUT"} {
		if got := b.Send(method, path, url.Values{"csrf_token": {b.Cookie(auth.CSRFCookie)}}); got.Code != 405 {
			t.Fatalf("%s Undo: %d", method, got.Code)
		}
	}
	if got := b.Send("DELETE", path, url.Values{"csrf_token": {b.Cookie(auth.CSRFCookie)}}); got.Code != 403 {
		t.Fatal("DELETE bypassed mutation protection", got.Code)
	}
	for _, values := range []url.Values{{}, {"csrf_token": {"wrong"}}} {
		if got := b.Send("POST", path, values); got.Code != 403 {
			t.Fatal("Undo bypassed CSRF", got.Code)
		}
	}
	stranger := fixture.NewAccountBrowser(t, a.Handler)
	stranger.Send("GET", "/login", nil)
	if got := stranger.Post(path, url.Values{}); got.Code != 303 {
		t.Fatal("anonymous Undo allowed", got.Code)
	}
	stranger.Send("GET", "/register", nil)
	if got := stranger.Post("/register", fixture.RegisterValues("undo-stranger@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := stranger.Post(path, url.Values{}); got.Code != 404 || strings.Contains(got.Body.String(), "Hello") {
		t.Fatal("another owner accessed Undo", got.Code)
	}
	for _, path := range []string{"/bots/1/chats/2/runs/1/undo", "/bots/1/chats/1/runs/999/undo", "/bots/1/chats/1/runs/0/undo", "/bots/1/chats/1/runs/nope/undo", "/bots/2/chats/1/runs/1/undo"} {
		if got := b.Post(path, url.Values{}); got.Code != 404 {
			t.Fatalf("mismatched Undo %s: %d", path, got.Code)
		}
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected requests changed Draft")
	}
}

func TestBuilderUndoSurvivesRestartWithoutProviderCredentials(t *testing.T) {
	a, b := fixture.UndoFixture(t)
	before := b.LoadDraft(t, 1)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	a.StopWork()
	a.Builder.Wait()
	_ = a.DB.Close()
	cfg := a.Config
	cfg.Builder.APIKey = ""
	restarted, err := fixture.New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	path := "/bots/1/chats/1/runs/1/undo"
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `action="`+path+`"`) {
		t.Fatal("restart or absent provider lost Undo")
	}
	if got := b.Post(path, url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before.Set("draft_revision", "3")
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("restart lost preceding Draft snapshot")
	}
	restarted.StopWork()
	restarted.Builder.Wait()
	_ = restarted.DB.Close()
	second, err := fixture.New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.StopWork(); second.Builder.Wait(); _ = second.DB.Close() })
	b.Router = second.Handler
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-run-result="undone"`) || !strings.Contains(page, "بازگردانده شد") {
		t.Fatal("restart lost successful Undo outcome")
	}
	if got := b.Post(path, url.Values{}); got.Code != 409 {
		t.Fatal("restart permitted repeated Undo")
	}
}

func TestBuilderUndoRacesAcrossAppInstancesOnlyRestoreOnce(t *testing.T) {
	a, b := fixture.UndoFixture(t)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	second, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.StopWork(); second.Builder.Wait(); _ = second.DB.Close() })
	other := fixture.NewAccountBrowser(t, second.Handler, second.Config.DatabasePath)
	other.Jar = b.Jar
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, browser := range []*fixture.Browser{b, other} {
		wg.Go(func() {
			<-start
			codes <- browser.Post("/bots/1/chats/1/runs/1/undo", url.Values{}).Code
		})
	}
	close(start)
	wg.Wait()
	first, last := <-codes, <-codes
	if first+last != 303+409 || (first != 303 && first != 409) {
		t.Fatalf("Undo race: %d %d", first, last)
	}
	if after := b.LoadDraft(t, 1); after.Get("draft_revision") != "3" {
		t.Fatal("racing Undo advanced Draft more than once")
	}
}

func TestBuilderUndoRacesWithManualSaveWithoutLosingNewerWork(t *testing.T) {
	a, b := fixture.UndoFixture(t)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	manual := b.LoadDraft(t, 1)
	manual.Set("welcome", "کار جدید مالک")
	other := fixture.NewAccountBrowser(t, a.Handler, a.Config.DatabasePath)
	other.Jar = b.Jar
	start := make(chan struct{})
	undoCode, manualResult := make(chan int, 1), make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() { <-start; undoCode <- b.Post("/bots/1/chats/1/runs/1/undo", url.Values{}).Code })
	wg.Go(func() { <-start; manualResult <- other.SaveDraft(t, 1, manual) })
	close(start)
	wg.Wait()
	u, m := <-undoCode, <-manualResult
	if !((u == 303 && errors.Is(m, bot.ErrStaleDraft)) || (u == 409 && m == nil)) {
		t.Fatalf("draft/Undo race: %d %v", u, m)
	}
	after := b.LoadDraft(t, 1)
	if after.Get("draft_revision") != "3" || (m == nil && after.Get("welcome") != "کار جدید مالک") {
		t.Fatal("Undo race lost a committed manual change")
	}
}

func TestBuilderUndoInvalidatesPendingBuilderResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n%2 == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
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
		fixture.BuilderTextReply(w)
	})
	defer close(release)
	before := b.LoadDraft(t, 1)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	if got := b.Post("/bots/1/chats/2/messages", url.Values{"message": {"ویرایش بعدی"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("pending Builder run did not stage its change")
	}
	if got := b.Post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	release <- struct{}{}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/2", "failed")
	if !strings.Contains(page, `data-run-result="conflict"`) || strings.Contains(page, "/undo\"") {
		t.Fatal("pending result ignored Undo's fresh revision")
	}
	before.Set("draft_revision", "3")
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("pending Builder overwrote Undo")
	}
}

func TestBuilderUndoUnavailableForTurnsWithoutSuccessfulChange(t *testing.T) {
	for _, kind := range []string{"conversation", "invalid", "provider failure", "stopped", "interrupted"} {
		t.Run(kind, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			var calls atomic.Int64
			a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 && kind != "conversation" && kind != "provider failure" {
					candidate := fixture.StructuredDraft
					if kind == "invalid" {
						candidate = `{}`
					}
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": candidate})
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
				fixture.BuilderTextReply(w)
			})
			before := b.LoadDraft(t, 1)
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست"}}); got.Code != 303 {
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
					if got := b.Post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
						t.Fatal(got.Code)
					}
					status = "stopped"
				} else {
					a.StopWork()
					a.Builder.Wait()
					_ = a.DB.Close()
					restarted, err := fixture.New(t.Context(), a.Config)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
					b.Router = restarted.Handler
					status = "interrupted"
				}
			}
			page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", status)
			if strings.Contains(page, "/undo\"") {
				t.Fatal("turn without a saved change offers Undo")
			}
			if got := b.Post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 409 {
				t.Fatal("turn without a saved change allowed Undo", got.Code)
			}
			if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
				t.Fatal("unavailable Undo changed Draft")
			}
		})
	}
}

func TestBuilderUndoStorageFailureRollsBackAndRemainsAvailable(t *testing.T) {
	a, b := fixture.UndoFixture(t)
	fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	before := b.LoadDraft(t, 1)
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_undo_feedback BEFORE INSERT ON builder_messages WHEN NEW.content='builder.run.undone' BEGIN SELECT RAISE(ABORT,'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	path := "/bots/1/chats/1/runs/1/undo"
	if got := b.Post(path, url.Values{}); got.Code != 500 {
		t.Fatal("failed Undo was reported successful", got.Code)
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("failed feedback left a partial restoration")
	}
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `action="`+path+`"`) || strings.Contains(page, `data-run-result="undone"`) {
		t.Fatal("failed Undo consumed the saved outcome")
	}
	if _, err := a.DB.Exec("DROP TRIGGER fail_undo_feedback"); err != nil {
		t.Fatal(err)
	}
	if got := b.Post(path, url.Values{}); got.Code != 303 {
		t.Fatal("retry after storage recovery failed", got.Code)
	}
}

func TestBuilderUndoRestoresDraftAtFreshRevisionOnce(t *testing.T) {
	_, b := fixture.UndoFixture(t)
	before := b.LoadDraft(t, 1)
	page := fixture.SaveBuilderChange(t, b, "/bots/1/chats/1")
	path := "/bots/1/chats/1/runs/1/undo"
	if !strings.Contains(page, `action="`+path+`"`) {
		t.Fatal("successful change offers no Undo")
	}
	// A late Stop must leave the applied change and its Undo available.
	if got := b.Post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post(path, url.Values{}); got.Code != 303 {
		t.Fatalf("Undo: %d", got.Code)
	}
	before.Set("draft_revision", "3")
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("Undo did not restore the preceding Draft with a fresh revision")
	}
	page = b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(page, `data-run-result="undone"`) || strings.Contains(page, `action="`+path+`"`) || !strings.Contains(page, "بازگردانده شد") {
		t.Fatal("Undo outcome is not honest or durable")
	}
	if got := b.Post(path, url.Values{}); got.Code != 409 {
		t.Fatalf("repeated Undo: %d", got.Code)
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("repeated Undo mutated the Draft")
	}
}
