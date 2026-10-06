package builder_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestStudioFormsReturnCommittedFragmentsWithoutNavigation(t *testing.T) {
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) { fixture.MemoryReply(w, "پاسخ ذخیره شده") })
	got := fixture.StudioRequest(b, "POST", "/chats", url.Values{"message": {"پیکو چه می کند؟"}, "request_key": {"fragment-start"}})
	if got.Code != 200 || got.Header().Get("X-Piko-Accepted") != "true" || !strings.Contains(got.Body.String(), `data-chat-url="/chats/1"`) || strings.Contains(got.Body.String(), "<html") || strings.Contains(got.Body.String(), "<script") {
		t.Fatalf("first admission navigates instead of returning a fragment: %d", got.Code)
	}
	if strings.Contains(got.Body.String(), `name="request_key" value="fragment-start"`) {
		t.Fatal("accepted fragment reused a consumed request key")
	}
	fixture.WaitBuilder(t, b, "/chats/1", "succeeded")
	got = fixture.StudioRequest(b, "POST", "/chats/1/messages", url.Values{"message": {" "}})
	if got.Code != 422 || got.Header().Get("X-Piko-Accepted") != "" || !strings.Contains(got.Body.String(), "data-piko-studio") {
		t.Fatal("validation did not return an editable fragment")
	}
	got = fixture.StudioRequest(b, "POST", "/chats/1/messages", url.Values{"message": {"پیام دوم"}, "request_key": {"fragment-second"}})
	if got.Code != 200 || got.Header().Get("X-Piko-Accepted") != "true" {
		t.Fatal("follow-up admission navigates")
	}
	fixture.WaitBuilder(t, b, "/chats/1", "succeeded")
	if got = fixture.StudioRequest(b, "POST", "/chats/1/messages", url.Values{"message": {"بدون مجوز"}, "csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("fragment bypassed CSRF")
	}
}

func TestStudioIdentifiesOtherChatWorkAndRefreshesAvailability(t *testing.T) {
	started := make(chan struct{})
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	})
	defer b.Post("/bots/1/chats/1/runs/1/stop", url.Values{})
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"کار در گفتگوی اول"}})
	<-started
	got := fixture.StudioRequest(b, "GET", "/bots/1/chats/2", nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `data-builder-stream-url="/bots/1/chats/1/stream"`) || !strings.Contains(got.Body.String(), `data-studio-busy`) || !strings.Contains(got.Body.String(), `data-can-send="false"`) {
		t.Fatal("other-chat active work is not identified and observed")
	}
	got = fixture.StudioRequest(b, "POST", "/bots/1/chats/1/runs/1/stop", url.Values{})
	if got.Code != 200 || !strings.Contains(got.Body.String(), `data-run-status="stopped"`) || strings.Contains(got.Body.String(), `/runs/1/retry`) {
		t.Fatal("Stop did not return its accurate terminal fragment")
	}
	got = fixture.StudioRequest(b, "GET", "/bots/1/chats/2", nil)
	if strings.Contains(got.Body.String(), `data-studio-busy`) || !strings.Contains(got.Body.String(), `data-can-send="true"`) {
		t.Fatal("stopped other-chat work still blocks the composer")
	}
}

func TestStudioProvisionalCandidateAndConflictKeepCommittedDraft(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
		close(started)
		select {
		case <-release:
			fixture.MemoryReply(w, "پاسخ نهایی")
		case <-r.Context().Done():
		}
	})
	defer close(release)
	fixture.StudioRequest(b, "POST", "/bots/1/chats/1/messages", url.Values{"message": {"درخواست تغییر"}})
	<-started
	provisional := fixture.StudioRequest(b, "GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(provisional, `data-draft-revision="1"`) || strings.Contains(provisional, `data-after-revision=`) {
		t.Fatal("candidate represented as committed Draft")
	}
	if pane := fixture.ChangesPane(t, provisional); strings.Contains(pane, `data-change-action=`) || strings.Contains(pane, `/runs/1/undo`) {
		t.Fatal("provisional candidate leaked into Changes")
	}
	if err := b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.InquiryDraft(), "1")); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	got := fixture.StudioRequest(b, "GET", "/bots/1/chats/1", nil)
	body := got.Body.String()
	if !strings.Contains(body, `data-studio-outcome="failed"`) || !strings.Contains(body, `data-run-result="conflict"`) || !strings.Contains(body, `data-draft-revision="2"`) || strings.Contains(body, `/runs/1/retry`) || !strings.Contains(body, `data-can-send="true"`) {
		t.Fatal("terminal conflict fragment misrepresents committed state or recovery")
	}
	if pane := fixture.ChangesPane(t, body); !strings.Contains(pane, `data-change-result="conflict"`) || strings.Contains(pane, `data-change-action=`) || strings.Contains(pane, `/runs/1/undo`) {
		t.Fatal("conflict represented stale differences as saved Changes")
	}
}

func TestStudioInterruptedFragmentRequiresExplicitRecovery(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		fixture.MemoryReply(w, "پاسخ بازیابی")
	})
	path := fixture.StartPikoChat(t, b)
	fixture.StudioRequest(b, "POST", path+"/messages", url.Values{"message": {"درخواست ماندگار"}})
	<-started
	a.StopWork()
	a.Builder.Wait()
	_ = a.DB.Close()
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	for range 2 {
		body := fixture.StudioRequest(b, "GET", path, nil).Body.String()
		if !strings.Contains(body, `data-studio-outcome="interrupted"`) || !strings.Contains(body, `/runs/1/retry`) || calls.Load() != 1 {
			t.Fatal("interruption missing or read replayed request")
		}
	}
	got := fixture.StudioRequest(b, "POST", path+"/runs/1/retry", url.Values{})
	if got.Code != 200 || got.Header().Get("X-Piko-Accepted") != "true" {
		t.Fatal("explicit recovery navigates")
	}
	fixture.WaitBuilder(t, b, path, "succeeded")
	if calls.Load() != 2 {
		t.Fatal("recovery replayed more than once")
	}
}
