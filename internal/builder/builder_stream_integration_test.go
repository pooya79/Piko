package builder_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderStopBeforeCommitPreservesDraftAndAccounting(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not stage candidate")
	}
	path := "/bots/1/chats/1/runs/1/stop"
	if got := b.Send("GET", path, nil); got.Code != 405 {
		t.Fatal("Stop requires POST", got.Code)
	}
	if got := b.Send("POST", path, url.Values{}); got.Code != 403 {
		t.Fatal("Stop requires CSRF", got.Code)
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("stop-other@example.test", "دیگری", "OwnerPassword123"))
	if got := other.Post(path, url.Values{}); got.Code != 404 {
		t.Fatal("other owner stopped run", got.Code)
	}
	if got := b.Post("/bots/1/chats/2/runs/1/stop", url.Values{}); got.Code != 404 {
		t.Fatal("wrong chat stopped run", got.Code)
	}
	if got := b.Post(path, url.Values{}); got.Code != 303 {
		t.Fatal("Stop rejected", got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop left provider work running")
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "stopped")
	if strings.Contains(page, `data-after-revision=`) || !strings.Contains(page, `data-admitted="1"`) || !strings.Contains(page, `data-total-tokens="5"`) {
		t.Fatal("Stop lost accounting or saved provisional changes")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("stopped candidate changed Draft")
	}
	if got := b.Post(path, url.Values{}); got.Code != 303 {
		t.Fatal("repeated Stop is not idempotent")
	}
	if got := b.Post("/bots/1/chats/1/runs/1/retry", url.Values{}); got.Code != 409 {
		t.Fatal("stopped work was retried as interrupted", got.Code)
	}
}

func TestBuilderActiveChatStopCancelsWorkAndKeepsPriorDraftAndUsage(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1, 3:
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
		case 2:
			fixture.BuilderTextReply(w)
		case 4:
			close(started)
			<-r.Context().Done()
			close(cancelled)
		}
	})
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"تغییر بده"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	if got := b.Post("/bots/1/chats/1/runs/2/stop", url.Values{}); got.Code != 303 {
		t.Fatal("active Stop rejected", got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("stopped chat still has provider work")
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "stopped")
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("Stop reversed saved work or applied late candidate")
	}
	page := b.Send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(page, `data-admitted="2"`) || !strings.Contains(page, `data-total-tokens="17"`) {
		t.Fatal("Stop erased local accounting")
	}
}

func TestBuilderStopCompletionRaceReportsOnlyCommittedChanges(t *testing.T) {
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
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
					fixture.BuilderTextReply(w)
				case <-r.Context().Done():
				}
			})
			before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
			b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider did not stage")
			}
			path := "/bots/1/chats/1/runs/1/stop"
			if i == 0 {
				close(release)
				fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			} else if i == 1 {
				if got := b.Post(path, url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
				close(release)
			} else {
				close(release)
			}
			if got := b.Post(path, url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
			after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
			if strings.Contains(page, `data-run-status="succeeded"`) {
				if before == after || !strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-after-revision="2"`) {
					t.Fatal("committed success reported incorrectly")
				}
			} else if strings.Contains(page, `data-run-status="stopped"`) {
				if before != after || strings.Contains(page, `data-after-revision=`) || strings.Contains(page, "منو را آماده کردم") {
					t.Fatal("stopped work wrote a result")
				}
			} else {
				t.Fatal("Stop race has no accurate terminal result")
			}
		})
	}
}

func TestBuilderStreamFailureKeepsReportedUsageWithoutSavingPartialReply(t *testing.T) {
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"PARTIAL-REPLY","reasoning":"PRIVATE-REASONING"}}]}

data: {"choices":[],"usage":{"total_tokens":13,"cost":0.0007}}

data: {"error":{"message":"private provider failure"}}

`)
	})
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if strings.Contains(page, "PARTIAL-REPLY") || strings.Contains(page, "PRIVATE-REASONING") || strings.Contains(page, "private provider") || !strings.Contains(page, `data-total-tokens="13"`) || !strings.Contains(page, `data-cost="0.0007"`) {
		t.Fatal("partial failure saved content or lost usage")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); before != after {
		t.Fatal("stream failure changed Draft")
	}
}

func TestBuilderStopFromAnotherAppFencesAndCancelsLeaseOwner(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not stage")
	}
	other, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.StopWork(); other.Builder.Wait(); _ = other.DB.Close() })
	b.Router = other.Handler
	if got := b.Post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("lease owner kept stopped provider work")
	}
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "stopped")
	if strings.Contains(page, `data-after-revision=`) || calls.Load() != 2 {
		t.Fatal("remote Stop permitted late work")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()).Encode(); before != after {
		t.Fatal("remote Stop changed Draft")
	}
}
