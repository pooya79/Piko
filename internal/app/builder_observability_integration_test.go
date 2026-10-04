package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type traceReceiver struct {
	mu       sync.Mutex
	spans    []*tracepb.Span
	requests int
	failure  int
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func TestBuilderLangfuseEnvironmentConfigurationNeverDisablesWork(t *testing.T) {
	for _, tc := range []struct {
		name                string
		url, public, secret bool
		invalidURL, capture string
		export, warning     bool
	}{
		{name: "absent"},
		{name: "URL only", url: true, warning: true},
		{name: "keys only", public: true, secret: true, warning: true},
		{name: "missing secret", url: true, public: true, warning: true},
		{name: "missing public", url: true, secret: true, warning: true},
		{name: "invalid URL", public: true, secret: true, invalidURL: "https://user:URL-secret@invalid.test", warning: true},
		{name: "complete", url: true, public: true, secret: true, export: true},
		{name: "content opt-in", url: true, public: true, secret: true, capture: "true", export: true},
		{name: "invalid content opt-in", url: true, public: true, secret: true, capture: "invalid", export: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &traceReceiver{}
			server := receiver.serve(t)
			t.Setenv("APP_ENV", "development")
			t.Setenv("DATABASE_PATH", "unused-by-fixture.db")
			t.Setenv("SESSION_SECRET", "builder-test-secret-at-least-32-characters")
			t.Setenv("BOT_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
			for _, key := range []string{"LANGFUSE_BASE_URL", "LANGFUSE_PUBLIC_KEY", "LANGFUSE_SECRET_KEY", "LANGFUSE_CAPTURE_CONTENT"} {
				t.Setenv(key, "")
			}
			if tc.url {
				t.Setenv("LANGFUSE_BASE_URL", server.URL)
			}
			if tc.invalidURL != "" {
				t.Setenv("LANGFUSE_BASE_URL", tc.invalidURL)
			}
			if tc.public {
				t.Setenv("LANGFUSE_PUBLIC_KEY", "monitor-public")
			}
			if tc.secret {
				t.Setenv("LANGFUSE_SECRET_KEY", "monitor-secret")
			}
			t.Setenv("LANGFUSE_CAPTURE_CONTENT", tc.capture)
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatal("monitoring config disabled App", err)
			}
			// The operator warning is observable through the App's normal log sink.
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			stdout := os.Stdout
			os.Stdout = writer
			defer func() { os.Stdout = stdout; _ = reader.Close(); _ = writer.Close() }()
			a, b := builderFixtureWithLogLevel(t, cfg.Builder, "warn", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"configuration reply"},"finish_reason":"stop"}]}`))
			})
			os.Stdout = stdout
			_ = writer.Close()
			logged, _ := io.ReadAll(reader)
			if strings.Contains(string(logged), "tracing disabled") != tc.warning {
				t.Fatal("operator warning missing or unexpected", string(logged))
			}
			for _, secret := range []string{"monitor-public", "monitor-secret", "URL-secret"} {
				if strings.Contains(string(logged), secret) {
					t.Fatal("warning leaked credential")
				}
			}
			stop := startBuilderApp(t, a)
			defer stop()
			if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"configuration prompt"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			stop()
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if (receiver.requests > 0) != tc.export {
				t.Fatal("configuration did not control export", receiver.requests)
			}
			content := ""
			for _, span := range receiver.spans {
				content += spanAttributes(span)["langfuse.observation.input"]
			}
			if strings.Contains(content, "configuration prompt") != (tc.capture == "true") {
				t.Fatal("content opt-in ignored")
			}
		})
	}
}

func (receiver *traceReceiver) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/public/otel/v1/traces" {
			t.Error("unexpected monitoring request", r.Method, r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "monitor-public" || password != "monitor-secret" {
			t.Error("missing OTLP authentication")
		}
		body, _ := io.ReadAll(r.Body)
		var request collector.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			t.Error(err)
		}
		receiver.mu.Lock()
		receiver.requests++
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				receiver.spans = append(receiver.spans, scope.Spans...)
			}
		}
		receiver.mu.Unlock()
		if receiver.started != nil {
			receiver.once.Do(func() { close(receiver.started) })
		}
		if receiver.release != nil {
			select {
			case <-receiver.release:
			case <-r.Context().Done():
				return
			}
		}
		if receiver.failure != 0 {
			w.WriteHeader(receiver.failure)
			_, _ = w.Write([]byte("monitor-secret private monitoring failure"))
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	t.Cleanup(server.Close)
	return server
}

func TestBuilderMonitoringOutagesPreserveDraftAccountingAndBoundAppShutdown(t *testing.T) {
	for _, failure := range []string{"unavailable", "stalled"} {
		t.Run(failure, func(t *testing.T) {
			receiver := &traceReceiver{failure: 503, started: make(chan struct{})}
			if failure == "stalled" {
				receiver.release = make(chan struct{})
				defer close(receiver.release)
			}
			server := receiver.serve(t)
			var calls atomic.Int64
			a, b := builderFixture(t, builder.Config{DailyRequests: 2, Langfuse: builder.TraceConfig{BaseURL: server.URL, PublicKey: "monitor-public", SecretKey: "monitor-secret"}}, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1)%2 == 1 {
					builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"saved despite monitoring outage"},"finish_reason":"stop"}],"usage":{"total_tokens":0,"cost":0}}`))
			})
			stop := startBuilderApp(t, a)
			defer stop()
			for _, chat := range []string{"1", "2"} {
				if got := b.post("/bots/1/chats/"+chat+"/messages", url.Values{"message": {"prepare Draft"}}); got.Code != 303 {
					t.Fatal(got.Code)
				}
				page := waitBuilder(t, b, "/bots/1/chats/"+chat, "succeeded")
				if !strings.Contains(page, `data-draft-revision="`+map[string]string{"1": "2", "2": "3"}[chat]+`"`) {
					t.Fatal("outage prevented Draft application")
				}
				if chat == "1" {
					select {
					case <-receiver.started:
					case <-time.After(3 * time.Second):
						t.Fatal("export did not start")
					}
				}
			}
			if got := b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			page := b.send("GET", "/bots/1/chats/2", nil).Body.String()
			if !strings.Contains(page, `data-admitted="2"`) || !strings.Contains(page, `data-total-tokens="10"`) || !strings.Contains(page, `data-cost="0"`) {
				t.Fatal("deletion or monitoring lost local accounting")
			}
			if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"over budget"}}); got.Code != 429 || calls.Load() != 4 {
				t.Fatal("monitoring outage bypassed local limits")
			}
			started := time.Now()
			stop()
			if time.Since(started) > a.cfg.ShutdownPeriod+300*time.Millisecond {
				t.Fatal("monitoring kept App/SQLite open beyond shutdown budget")
			}
			if err := a.db.Ping(); err == nil {
				t.Fatal("App shutdown did not close SQLite")
			}
		})
	}
}

func TestBuilderExportsSafeNativeErrorsWithUnknownAccounting(t *testing.T) {
	receiver := &traceReceiver{}
	server := receiver.serve(t)
	a, b := builderFixture(t, builder.Config{Langfuse: builder.TraceConfig{BaseURL: server.URL, PublicKey: "monitor-public", SecretKey: "monitor-secret", CaptureContent: true}}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":{"message":"test-server-key monitor-secret private upstream description"}}`))
	})
	stop := startBuilderApp(t, a)
	defer stop()
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"fail safely"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "failed")
	if !strings.Contains(page, `data-total-tokens="unknown"`) {
		t.Fatal("unknown accounting lost")
	}
	stop()
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	errors := 0
	for _, span := range receiver.spans {
		attrs := spanAttributes(span)
		if attrs["langfuse.observation.type"] == "generation" && span.Status.GetCode() == tracepb.Status_STATUS_CODE_ERROR {
			errors++
		}
		for _, secret := range []string{"test-server-key", "monitor-secret", "private upstream description"} {
			if strings.Contains(span.String(), secret) {
				t.Fatal("upstream error/credential exported")
			}
		}
		if len(span.Events) != 0 {
			t.Fatal("raw Genkit error event exported")
		}
	}
	if errors != 1 {
		t.Fatal("native model error missing", errors)
	}
}

func TestBuilderTracingSelectsOnlyItsAppAndRejectsUnauthorizedWork(t *testing.T) {
	type workspace struct {
		stop     func()
		browser  *accountBrowser
		receiver *traceReceiver
		marker   string
	}
	workspaces := []workspace{}
	for _, marker := range []string{"first workspace private prompt", "second workspace private prompt"} {
		receiver := &traceReceiver{}
		server := receiver.serve(t)
		a, b := builderFixture(t, builder.Config{Langfuse: builder.TraceConfig{BaseURL: server.URL, PublicKey: "monitor-public", SecretKey: "monitor-secret", CaptureContent: true}}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"workspace reply"},"finish_reason":"stop"}]}`))
		})
		stop := startBuilderApp(t, a)
		defer stop()
		workspaces = append(workspaces, workspace{stop, b, receiver, marker})
	}
	for _, ws := range workspaces {
		other := newAccountBrowser(t, ws.browser.router)
		other.send("GET", "/register", nil)
		if got := other.post("/register", registerValues("trace-other@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
			t.Fatal(got.Code)
		}
		if got := other.post("/bots/1/chats/1/messages", url.Values{"message": {"unauthorized private prompt"}}); got.Code != 404 {
			t.Fatal("another owner admitted", got.Code)
		}
		if got := ws.browser.send("POST", "/bots/1/chats/1/messages", url.Values{"message": {"invalid CSRF private prompt"}, "csrf_token": {"invalid"}}); got.Code != 403 {
			t.Fatal("invalid CSRF admitted", got.Code)
		}
		if got := ws.browser.post("/bots/1/chats/1/messages", url.Values{"message": {ws.marker}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	for _, ws := range workspaces {
		waitBuilder(t, ws.browser, "/bots/1/chats/1", "succeeded")
	}
	for i, ws := range workspaces {
		ws.stop()
		ws.receiver.mu.Lock()
		content := ""
		for _, span := range ws.receiver.spans {
			content += span.String()
		}
		ws.receiver.mu.Unlock()
		if !strings.Contains(content, ws.marker) || strings.Contains(content, workspaces[1-i].marker) || strings.Contains(content, "unauthorized private prompt") || strings.Contains(content, "invalid CSRF private prompt") {
			t.Fatal("trace selection leaked a different App or rejected request")
		}
	}
}

func TestBuilderTraceReportsRevisionConflictAsFailedRun(t *testing.T) {
	receiver := &traceReceiver{}
	server := receiver.serve(t)
	prepared, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{Langfuse: builder.TraceConfig{BaseURL: server.URL, PublicKey: "monitor-public", SecretKey: "monitor-secret"}}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		close(prepared)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"prepared candidate"},"finish_reason":"stop"}]}`))
	})
	defer close(release)
	stop := startBuilderApp(t, a)
	defer stop()
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"prepare candidate"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-prepared:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate not prepared")
	}
	manual := inquiryDraft()
	manual.Set("welcome", "manual change stays")
	if got := b.post("/bots/1/draft", draftAtRevision(manual, "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	release <- struct{}{}
	waitBuilder(t, b, "/bots/1/chats/1", "failed")
	stop()
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	for _, span := range receiver.spans {
		if status, ok := spanAttributes(span)["piko.run.status"]; ok {
			if status != "failed" || span.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR {
				t.Fatal("trace reported success for a rejected Draft commit", status)
			}
			return
		}
	}
	t.Fatal("terminal run observation missing")
}

func spanAttributes(span *tracepb.Span) map[string]string {
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[attr.Key] = attr.Value.GetStringValue()
	}
	return attrs
}

func TestBuilderExportsActualGenkitModelAndToolSpansWithoutContent(t *testing.T) {
	receiver := &traceReceiver{}
	server := receiver.serve(t)
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{Langfuse: builder.TraceConfig{BaseURL: server.URL, PublicKey: "monitor-public", SecretKey: "monitor-secret"}}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			builderToolReply(w, "prepare_draft", map[string]string{"definition": builderFormDraft})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"private reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"cost":0}}`))
	})
	stop := startBuilderApp(t, a)
	defer stop()
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {"private prompt"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	if !strings.Contains(page, `data-after-revision="2"`) {
		t.Fatal("Draft was not applied")
	}
	stop()
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	models, tools, zero, reported := 0, 0, false, false
	for _, span := range receiver.spans {
		attrs := spanAttributes(span)
		if attrs["langfuse.session.id"] != "builder-chat-1" || attrs["langfuse.trace.metadata.run_id"] != "1" {
			t.Fatal("run/chat correlation missing")
		}
		if span.EndTimeUnixNano < span.StartTimeUnixNano || span.StartTimeUnixNano == 0 {
			t.Fatal("missing latency timestamps")
		}
		if attrs["langfuse.observation.type"] == "generation" {
			models++
			if attrs["genkit:metadata:subtype"] != "model" || attrs["langfuse.observation.model.name"] != "openai/gpt-6-luna" {
				t.Fatal("not a native Genkit model observation")
			}
			zero = zero || attrs["langfuse.observation.usage_details"] == `{"input":0,"output":0,"total":0}` && attrs["langfuse.observation.cost_details"] == `{"total":0}`
			reported = reported || attrs["langfuse.observation.usage_details"] == `{"total":5}`
			if attrs["piko.call.sequence"] != "1" && attrs["piko.call.sequence"] != "2" {
				t.Fatal("per-call correlation missing")
			}
			if attrs["langfuse.observation.usage_details"] == `{"input":0,"output":0,"total":0}` && attrs["langfuse.observation.metadata.provider_cost_reported"] != "true" {
				t.Fatal("reported cost source lost")
			}
		}
		if attrs["langfuse.observation.type"] == "tool" && span.Name == "prepare_draft" {
			tools++
		}
		if strings.Contains(span.String(), "private prompt") || strings.Contains(span.String(), "private reply") || strings.Contains(span.String(), "definition") {
			t.Fatal("content exported without opt-in")
		}
	}
	if models != 2 || tools == 0 || !zero || !reported {
		t.Fatalf("native observations/accounting missing: models=%d tools=%d zero=%v reported=%v", models, tools, zero, reported)
	}
}

func TestBuilderOptedInContentExcludesCredentialsAndOtherChats(t *testing.T) {
	receiver := &traceReceiver{}
	server := receiver.serve(t)
	a, b := builderFixture(t, builder.Config{Langfuse: builder.TraceConfig{BaseURL: server.URL, PublicKey: "monitor-public", SecretKey: "monitor-secret", CaptureContent: true}}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"visible answer monitor-secret test-server-key 123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZ_123456789"},"finish_reason":"stop"}]}`))
	})
	stop := startBuilderApp(t, a)
	defer stop()
	// A second chat's memory is deliberately distinct from the observed chat.
	if got := b.post("/bots/1/chats/2/messages", url.Values{"message": {"unrelated chat marker"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, "/bots/1/chats/2", "succeeded")
	secrets := []string{a.cfg.SessionSecret, a.cfg.BotEncryptionKey, "test-server-key", "monitor-public", "monitor-secret", "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZ_123456789", b.cookie("piko_session"), b.cookie("piko_csrf")}
	message := "visible owner request " + strings.Join(secrets, " ") + ` {"password":"short-json-secret"} token="short-labelled-secret" https://username:short-url-secret@example.test/path {"password":"correct horse battery staple"} cookie='spaced cookie private value'`
	secrets = append(secrets, "short-json-secret", "short-labelled-secret", "short-url-secret", "horse battery staple", "cookie private value")
	if got := b.post("/bots/1/chats/1/messages", url.Values{"message": {message}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	stop()
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	content := ""
	for _, span := range receiver.spans {
		attrs := spanAttributes(span)
		if attrs["langfuse.session.id"] != "builder-chat-1" {
			continue
		}
		encoded := span.String()
		for _, secret := range secrets {
			if secret != "" && strings.Contains(encoded, secret) {
				t.Fatalf("credential exported: length %d", len(secret))
			}
		}
		if strings.Contains(encoded, "unrelated chat marker") {
			t.Fatal("another chat entered trace content")
		}
		if attrs["langfuse.observation.type"] == "generation" {
			if attrs["langfuse.observation.metadata.provider_cost_reported"] != "false" || attrs["langfuse.observation.metadata.provider_total_tokens_reported"] != "false" {
				t.Fatal("unknown provider accounting source lost")
			}
			if _, ok := attrs["langfuse.observation.usage_details"]; ok {
				t.Fatal("unknown usage became zero")
			}
			if _, ok := attrs["langfuse.observation.cost_details"]; ok {
				t.Fatal("unknown cost became zero")
			}
			content += attrs["langfuse.observation.input"] + attrs["langfuse.observation.output"]
		}
	}
	if !strings.Contains(content, "visible owner request") || !strings.Contains(content, "visible answer") || !strings.Contains(content, "[REDACTED]") {
		t.Fatal("explicitly opted-in sanitized content missing")
	}
}
