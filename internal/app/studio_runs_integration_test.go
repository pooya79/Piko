package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
)

func studioRequest(b *accountBrowser, method, path string, values url.Values) *httptest.ResponseRecorder {
	if values == nil {
		values = url.Values{}
	}
	if method == "POST" && !values.Has("csrf_token") {
		values.Set("csrf_token", b.cookie("piko_csrf"))
	}
	r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	r.Header.Set("X-Piko-Studio", "fragment")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range b.jar.Cookies(b.base) {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	b.router.ServeHTTP(w, r)
	return w
}

func TestStudioFormsReturnCommittedFragmentsWithoutNavigation(t *testing.T) {
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) { memoryReply(w, "پاسخ ذخیره شده") })
	got := studioRequest(b, "POST", "/chats", url.Values{"message": {"پیکو چه می کند؟"}, "request_key": {"fragment-start"}})
	if got.Code != 200 || got.Header().Get("X-Piko-Accepted") != "true" || !strings.Contains(got.Body.String(), `data-chat-url="/chats/1"`) || strings.Contains(got.Body.String(), "<html") || strings.Contains(got.Body.String(), "<script") {
		t.Fatalf("first admission navigates instead of returning a fragment: %d", got.Code)
	}
	if strings.Contains(got.Body.String(), `name="request_key" value="fragment-start"`) {
		t.Fatal("accepted fragment reused a consumed request key")
	}
	waitBuilder(t, b, "/chats/1", "succeeded")
	got = studioRequest(b, "POST", "/chats/1/messages", url.Values{"message": {" "}})
	if got.Code != 422 || got.Header().Get("X-Piko-Accepted") != "" || !strings.Contains(got.Body.String(), "data-piko-studio") {
		t.Fatal("validation did not return an editable fragment")
	}
	got = studioRequest(b, "POST", "/chats/1/messages", url.Values{"message": {"پیام دوم"}, "request_key": {"fragment-second"}})
	if got.Code != 200 || got.Header().Get("X-Piko-Accepted") != "true" {
		t.Fatal("follow-up admission navigates")
	}
	waitBuilder(t, b, "/chats/1", "succeeded")
	if got = studioRequest(b, "POST", "/chats/1/messages", url.Values{"message": {"بدون مجوز"}, "csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("fragment bypassed CSRF")
	}
}

func TestStudioIdentifiesOtherChatWorkAndRefreshesAvailability(t *testing.T) {
	started := make(chan struct{})
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	})
	defer b.post("/bots/1/chats/1/runs/1/stop", url.Values{})
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"کار در گفتگوی اول"}})
	<-started
	got := studioRequest(b, "GET", "/bots/1/chats/2", nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `data-builder-stream-url="/bots/1/chats/1/stream"`) || !strings.Contains(got.Body.String(), `data-studio-busy`) || !strings.Contains(got.Body.String(), `data-can-send="false"`) {
		t.Fatal("other-chat active work is not identified and observed")
	}
	got = studioRequest(b, "POST", "/bots/1/chats/1/runs/1/stop", url.Values{})
	if got.Code != 200 || !strings.Contains(got.Body.String(), `data-run-status="stopped"`) || strings.Contains(got.Body.String(), `/runs/1/retry`) {
		t.Fatal("Stop did not return its accurate terminal fragment")
	}
	got = studioRequest(b, "GET", "/bots/1/chats/2", nil)
	if strings.Contains(got.Body.String(), `data-studio-busy`) || !strings.Contains(got.Body.String(), `data-can-send="true"`) {
		t.Fatal("stopped other-chat work still blocks the composer")
	}
}

func TestStudioProvisionalCandidateAndConflictKeepCommittedDraft(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		close(started)
		select {
		case <-release:
			memoryReply(w, "پاسخ نهایی")
		case <-r.Context().Done():
		}
	})
	defer close(release)
	studioRequest(b, "POST", "/bots/1/chats/1/messages", url.Values{"message": {"درخواست تغییر"}})
	<-started
	provisional := studioRequest(b, "GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(provisional, `data-draft-revision="1"`) || strings.Contains(provisional, `data-after-revision=`) {
		t.Fatal("candidate represented as committed Draft")
	}
	if pane := changesPane(t, provisional); strings.Contains(pane, `data-change-action=`) || strings.Contains(pane, `/runs/1/undo`) {
		t.Fatal("provisional candidate leaked into Changes")
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	release <- struct{}{}
	waitBuilder(t, b, "/bots/1/chats/1", "failed")
	got := studioRequest(b, "GET", "/bots/1/chats/1", nil)
	body := got.Body.String()
	if !strings.Contains(body, `data-studio-outcome="failed"`) || !strings.Contains(body, `data-run-result="conflict"`) || !strings.Contains(body, `data-draft-revision="2"`) || strings.Contains(body, `/runs/1/retry`) || !strings.Contains(body, `data-can-send="true"`) {
		t.Fatal("terminal conflict fragment misrepresents committed state or recovery")
	}
	if pane := changesPane(t, body); !strings.Contains(pane, `data-change-result="conflict"`) || strings.Contains(pane, `data-change-action=`) || strings.Contains(pane, `/runs/1/undo`) {
		t.Fatal("conflict represented stale differences as saved Changes")
	}
}

func TestStudioInterruptedFragmentRequiresExplicitRecovery(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	a, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			return
		}
		memoryReply(w, "پاسخ بازیابی")
	})
	path := startPikoChat(t, b)
	studioRequest(b, "POST", path+"/messages", url.Values{"message": {"درخواست ماندگار"}})
	<-started
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	for range 2 {
		body := studioRequest(b, "GET", path, nil).Body.String()
		if !strings.Contains(body, `data-studio-outcome="interrupted"`) || !strings.Contains(body, `/runs/1/retry`) || calls.Load() != 1 {
			t.Fatal("interruption missing or read replayed request")
		}
	}
	got := studioRequest(b, "POST", path+"/runs/1/retry", url.Values{})
	if got.Code != 200 || got.Header().Get("X-Piko-Accepted") != "true" {
		t.Fatal("explicit recovery navigates")
	}
	waitBuilder(t, b, path, "succeeded")
	if calls.Load() != 2 {
		t.Fatal("recovery replayed more than once")
	}
}

func TestStudioGeneralReplyStreamsBeforeCommittedHistory(t *testing.T) {
	for _, reply := range []string{"پاسخ موقت عمومی", `{"answer":"پاسخ موقت عمومی"`, `{name} پاسخ موقت عمومی`} {
		t.Run(reply, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			a, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": reply}}}})
				fmt.Fprintf(w, "data: %s\n\n", frame)
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" کامل\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			})
			defer close(release)
			stop := startBuilderApp(t, a)
			defer stop()
			path := startPikoChat(t, b)
			b.post(path+"/messages", url.Values{"message": {"امکانات پیکو چیست؟"}})
			<-started
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+a.server.Addr+path+"/stream", nil)
			for _, cookie := range b.jar.Cookies(b.base) {
				req.AddCookie(cookie)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			readBuilderEvent(t, scanner, "پاسخ موقت عمومی")
			if strings.Contains(studioRequest(b, "GET", path, nil).Body.String(), "پاسخ موقت عمومی") {
				t.Fatal("provisional general text was saved before completion")
			}
			release <- struct{}{}
			readBuilderEvent(t, scanner, "succeeded")
			if !strings.Contains(studioRequest(b, "GET", path, nil).Body.String(), "پاسخ موقت عمومی") {
				t.Fatal("committed general reply missing")
			}
		})
	}
}
