package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
)

func TestBuilderStopBeforeCommitPreservesDraftAndAccounting(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not stage candidate")
	}
	path := "/bots/1/chats/1/runs/1/stop"
	if got := b.send("GET", path, nil); got.Code != 405 {
		t.Fatal("Stop requires POST", got.Code)
	}
	if got := b.send("POST", path, url.Values{}); got.Code != 403 {
		t.Fatal("Stop requires CSRF", got.Code)
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("stop-other@example.test", "دیگری", "OwnerPassword123"))
	if got := other.post(path, url.Values{}); got.Code != 404 {
		t.Fatal("other owner stopped run", got.Code)
	}
	if got := b.post("/bots/1/chats/2/runs/1/stop", url.Values{}); got.Code != 404 {
		t.Fatal("wrong chat stopped run", got.Code)
	}
	if got := b.post(path, url.Values{}); got.Code != 303 {
		t.Fatal("Stop rejected", got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop left provider work running")
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "stopped")
	if strings.Contains(page, `data-after-revision=`) || !strings.Contains(page, `data-admitted="1"`) || !strings.Contains(page, `data-total-tokens="5"`) {
		t.Fatal("Stop lost accounting or saved provisional changes")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("stopped candidate changed Draft")
	}
	if got := b.post(path, url.Values{}); got.Code != 303 {
		t.Fatal("repeated Stop is not idempotent")
	}
	if got := b.post("/bots/1/chats/1/runs/1/retry", url.Values{}); got.Code != 409 {
		t.Fatal("stopped work was retried as interrupted", got.Code)
	}
}

func TestBuilderStreamDisconnectReconnectAndDeadline(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if !request.Stream {
			t.Error("provider request does not stream")
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"پاسخ موقت <script>\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" کامل\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6,\"cost\":0.001}}\n\ndata: [DONE]\n\n")
	})
	defer close(release)
	a.server.WriteTimeout = 30 * time.Millisecond
	stop := startBuilderApp(t, a)
	defer stop()
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"پاسخ بده"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not started")
	}
	streamURL := "http://" + a.server.Addr + "/bots/1/chats/1/stream"
	connect := func() *http.Response {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		t.Cleanup(cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", streamURL, nil)
		for _, cookie := range b.jar.Cookies(b.base) {
			req.AddCookie(cookie)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Content-Security-Policy") == "" || response.Header.Get("X-Request-ID") == "" {
			response.Body.Close()
			t.Fatal("stream lost security/middleware", response.StatusCode)
		}
		return response
	}
	first := connect()
	readBuilderEvent(t, bufio.NewScanner(first.Body), "پاسخ موقت")
	_ = first.Body.Close()
	time.Sleep(80 * time.Millisecond) // Exceed ordinary WriteTimeout; stream renews it.
	second := connect()
	defer second.Body.Close()
	scanner := bufio.NewScanner(second.Body)
	readBuilderEvent(t, scanner, "پاسخ موقت")
	if page := b.send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-run-status="running"`) || strings.Contains(page, `data-run-result="saved"`) {
		t.Fatal("disconnect stopped work or provisional result was committed")
	}
	if got := b.send("GET", "/bots/1/chats/2/stream", nil); got.Code != 200 || strings.Contains(got.Body.String(), "پاسخ موقت") {
		t.Fatal("stream leaked across chats")
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("stream-other@example.test", "دیگری", "OwnerPassword123"))
	if got := other.send("GET", "/bots/1/chats/1/stream", nil); got.Code != 404 {
		t.Fatal("stream leaked to another owner")
	}
	release <- struct{}{}
	readBuilderEvent(t, scanner, "succeeded")
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if calls.Load() != 1 || !strings.Contains(page, "پاسخ موقت &lt;script&gt; کامل") || !strings.Contains(page, `data-total-tokens="6"`) || !strings.Contains(page, `data-cost="0.001"`) {
		t.Fatal("reconnect replayed work or lost escaped result/accounting")
	}
}

func readBuilderEvent(t *testing.T, scanner *bufio.Scanner, contains string) string {
	t.Helper()
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, contains) {
			return line
		}
	}
	t.Fatal("stream ended before event", contains, scanner.Err())
	return ""
}

// Existing scripted completions also exercise the real SDK streaming contract.
// Explicit SSE handlers pass through immediately for provider-barrier tests.
func streamingBuilderProvider(provider http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := &builderProviderWriter{ResponseWriter: w}
		provider(out, r)
		if out.streaming {
			return
		}
		if out.code == 0 {
			out.code = 200
		}
		if out.code != 200 {
			w.WriteHeader(out.code)
			_, _ = w.Write(out.body.Bytes())
			return
		}
		var response map[string]json.RawMessage
		if json.Unmarshal(out.body.Bytes(), &response) != nil {
			w.WriteHeader(200)
			_, _ = w.Write(out.body.Bytes())
			return
		}
		var choices []map[string]json.RawMessage
		_ = json.Unmarshal(response["choices"], &choices)
		for i, choice := range choices {
			var message map[string]json.RawMessage
			_ = json.Unmarshal(choice["message"], &message)
			if tools, ok := message["tool_calls"]; ok {
				var calls []map[string]json.RawMessage
				_ = json.Unmarshal(tools, &calls)
				for j, call := range calls {
					call["index"] = json.RawMessage(fmt.Sprint(j))
				}
				message["tool_calls"], _ = json.Marshal(calls)
			}
			delta, _ := json.Marshal(message)
			delete(choice, "message")
			choice["delta"] = delta
			choice["index"] = json.RawMessage(fmt.Sprint(i))
		}
		response["choices"], _ = json.Marshal(choices)
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(response)
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}
}

type builderProviderWriter struct {
	http.ResponseWriter
	body      bytes.Buffer
	code      int
	streaming bool
}

func (w *builderProviderWriter) WriteHeader(code int) {
	w.code = code
	w.streaming = strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream")
	if w.streaming {
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *builderProviderWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	if w.streaming {
		return w.ResponseWriter.Write(p)
	}
	return w.body.Write(p)
}
func (w *builderProviderWriter) Flush() {
	if w.code == 0 {
		w.WriteHeader(200)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

func TestBuilderActiveChatDeletionCancelsWorkAndKeepsPriorDraftAndUsage(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1, 3:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
		case 2:
			builderTextReply(w)
		case 4:
			close(started)
			<-r.Context().Done()
			close(cancelled)
		}
	})
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"تغییر بده"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	if got := b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 303 {
		t.Fatal("active deletion rejected", got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("deleted chat still has provider work")
	}
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/1/stream"} {
		if got := b.send("GET", path, nil); got.Code != 404 {
			t.Fatal("deleted history returned", got.Code)
		}
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); after != before {
		t.Fatal("deletion reversed saved work or applied late candidate")
	}
	page := b.send("GET", "/bots/1/chats/2", nil).Body.String()
	if !strings.Contains(page, `data-admitted="2"`) || !strings.Contains(page, `data-total-tokens="17"`) {
		t.Fatal("deletion erased local accounting")
	}
}

func TestBuilderStopCompletionRaceReportsOnlyCommittedChanges(t *testing.T) {
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
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
					builderTextReply(w)
				case <-r.Context().Done():
				}
			})
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
			b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider did not stage")
			}
			path := "/bots/1/chats/1/runs/1/stop"
			if i == 0 {
				close(release)
				waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			} else if i == 1 {
				if got := b.post(path, url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
				close(release)
			} else {
				close(release)
			}
			if got := b.post(path, url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := b.send("GET", "/bots/1/chats/1", nil).Body.String()
			after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
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

func TestBuilderStreamShutdownCancelsAndJoinsConnections(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"در حال کار\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	stop := startBuilderApp(t, a)
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not started")
	}
	req, _ := http.NewRequestWithContext(t.Context(), "GET", "http://"+a.server.Addr+"/bots/1/chats/1/stream", nil)
	for _, cookie := range b.jar.Cookies(b.base) {
		req.AddCookie(cookie)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	readBuilderEvent(t, scanner, "در حال کار")
	began := time.Now()
	stop()
	if time.Since(began) > 2*time.Second {
		t.Fatal("stream blocked bounded shutdown")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("shutdown left provider running")
	}
	for scanner.Scan() {
	}
	if err := a.db.Ping(); err == nil {
		t.Fatal("shutdown did not close SQLite after joining work")
	}
}

func TestBuilderStreamFailureKeepsReportedUsageWithoutSavingPartialReply(t *testing.T) {
	_, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"PARTIAL-REPLY","reasoning":"PRIVATE-REASONING"}}]}

data: {"choices":[],"usage":{"total_tokens":13,"cost":0.0007}}

data: {"error":{"message":"private provider failure"}}

`)
	})
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if strings.Contains(page, "PARTIAL-REPLY") || strings.Contains(page, "PRIVATE-REASONING") || strings.Contains(page, "private provider") || !strings.Contains(page, `data-total-tokens="13"`) || !strings.Contains(page, `data-cost="0.0007"`) {
		t.Fatal("partial failure saved content or lost usage")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); before != after {
		t.Fatal("stream failure changed Draft")
	}
}

func TestBuilderStopFromAnotherAppFencesAndCancelsLeaseOwner(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode()
	b.post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not stage")
	}
	other, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.stopRequests(); other.builder.Wait(); _ = other.db.Close() })
	b.router = other.server.Handler
	if got := b.post("/bots/1/chats/1/runs/1/stop", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("lease owner kept stopped provider work")
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "stopped")
	if strings.Contains(page, `data-after-revision=`) || calls.Load() != 2 {
		t.Fatal("remote Stop permitted late work")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()).Encode(); before != after {
		t.Fatal("remote Stop changed Draft")
	}
}
