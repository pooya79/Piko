package app

import (
	"bufio"
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
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

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
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"پاسخ بده"}}); got.Code != 303 {
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
		for _, cookie := range b.Jar.Cookies(b.Base) {
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
	if page := b.Send("GET", "/bots/1/chats/1", nil).Body.String(); !strings.Contains(page, `data-run-status="running"`) || strings.Contains(page, `data-run-result="saved"`) {
		t.Fatal("disconnect stopped work or provisional result was committed")
	}
	if got := b.Send("GET", "/bots/1/chats/2/stream", nil); got.Code != 200 || strings.Contains(got.Body.String(), "پاسخ موقت") {
		t.Fatal("stream leaked across chats")
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("stream-other@example.test", "دیگری", "OwnerPassword123"))
	if got := other.Send("GET", "/bots/1/chats/1/stream", nil); got.Code != 404 {
		t.Fatal("stream leaked to another owner")
	}
	release <- struct{}{}
	readBuilderEvent(t, scanner, "succeeded")
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
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
	b.Post("/bots/1/chats/1/messages", url.Values{"message": {"بساز"}})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider not started")
	}
	req, _ := http.NewRequestWithContext(t.Context(), "GET", "http://"+a.server.Addr+"/bots/1/chats/1/stream", nil)
	for _, cookie := range b.Jar.Cookies(b.Base) {
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
