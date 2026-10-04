package builder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/firebase/genkit/go/core/tracing"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TraceConfig is optional and deliberately does not participate in Builder
// validation: monitoring misconfiguration must leave manual and AI work available.
type TraceConfig struct {
	BaseURL, PublicKey, SecretKey string
	CaptureContent                bool
}

type observationContextKey struct{}
type observability struct {
	provider  *sdktrace.TracerProvider
	batch     sdktrace.SpanProcessor
	capture   bool
	id, model string
	secrets   []string
	once      sync.Once
}

// ConfigureTracing registers on Genkit's real provider. Each processor selects
// only this Service's admitted run context; another App in the process cannot
// leak its chats into this exporter's tenant. No baggage crosses upstream HTTP.
func (s *Service) ConfigureTracing(log *slog.Logger, secrets ...string) {
	c := s.config.Langfuse
	c.BaseURL, c.PublicKey, c.SecretKey = strings.TrimSpace(c.BaseURL), strings.TrimSpace(c.PublicKey), strings.TrimSpace(c.SecretKey)
	if c.BaseURL == "" && c.PublicKey == "" && c.SecretKey == "" {
		return
	}
	u, err := url.Parse(c.BaseURL)
	if c.BaseURL == "" || c.PublicKey == "" || c.SecretKey == "" || err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(c.PublicKey+c.SecretKey, "\r\n") || strings.Contains(c.PublicKey, ":") {
		log.Warn("Langfuse configuration incomplete or invalid; tracing disabled")
		return
	}
	if !s.Enabled() {
		return
	}
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(strings.TrimRight(c.BaseURL, "/")+"/api/public/otel/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(c.PublicKey+":"+c.SecretKey)), "x-langfuse-ingestion-version": "4"}),
		otlptracehttp.WithHTTPClient(client), otlptracehttp.WithTimeout(time.Second),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		log.Warn("Langfuse exporter unavailable; tracing disabled")
		return
	}
	o := &observability{provider: tracing.TracerProvider(), capture: c.CaptureContent, id: uuid.NewString(), model: s.config.Model, secrets: append(append([]string{}, secrets...), s.config.APIKey, c.PublicKey, c.SecretKey)}
	o.batch = sdktrace.NewBatchSpanProcessor(&safeExporter{SpanExporter: exporter, log: log}, sdktrace.WithMaxQueueSize(128), sdktrace.WithMaxExportBatchSize(32), sdktrace.WithBatchTimeout(100*time.Millisecond), sdktrace.WithExportTimeout(time.Second))
	o.provider.RegisterSpanProcessor(o)
	s.observability = o
}

func (o *observability) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if ctx.Value(observationContextKey{}) != o {
		return
	}
	run, ok := ctx.Value(runContextKey{}).(admittedRun)
	if !ok {
		return
	}
	span.SetAttributes(attribute.String("piko.observer", o.id),
		attribute.String("piko.model", o.model),
		attribute.String("langfuse.session.id", "builder-chat-"+strconv.FormatInt(run.chatID, 10)),
		attribute.String("langfuse.trace.metadata.run_id", strconv.FormatInt(run.id, 10)),
		attribute.String("langfuse.trace.metadata.bot_id", strconv.FormatInt(run.botID, 10)))
}

func (o *observability) OnEnd(span sdktrace.ReadOnlySpan) {
	attrs := make(map[string]attribute.Value)
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value
	}
	if attrs["piko.observer"].AsString() != o.id {
		return
	}
	safe := &exportSpan{ReadOnlySpan: span, name: "Builder", status: sdktrace.Status{Code: span.Status().Code}}
	add := func(k, v string) { safe.attrs = append(safe.attrs, attribute.String(k, v)) }
	for _, k := range []string{"langfuse.session.id", "langfuse.trace.metadata.run_id", "langfuse.trace.metadata.bot_id"} {
		add(k, attrs[k].AsString())
	}
	add("langfuse.trace.name", "Builder run")
	subtype := attrs["genkit:metadata:subtype"].AsString()
	kind := "span"
	switch subtype {
	case "model":
		kind = "generation"
		safe.name = "OpenRouter model"
		add("langfuse.observation.model.name", o.clean(attrs["piko.model"].AsString()))
	case "tool", "tool.v2":
		kind = "tool"
		// Names are application-owned, never model-provided identifiers.
		switch span.Name() {
		case "read_draft", "read_templates", "validate_draft", "prepare_draft":
			safe.name = span.Name()
		}
	case "util":
		safe.name = "Genkit generate"
	}
	add("langfuse.observation.type", kind)
	if subtype == "tool" || subtype == "tool.v2" || subtype == "model" || subtype == "util" {
		add("genkit:metadata:subtype", subtype)
	}
	for _, k := range []string{"langfuse.observation.usage_details", "langfuse.observation.cost_details", "piko.call.sequence", "piko.run.status"} {
		if value, ok := attrs[k]; ok {
			add(k, value.AsString())
		}
	}
	for _, metric := range []string{"input_tokens", "output_tokens", "total_tokens", "cost"} {
		key := "langfuse.observation.metadata.provider_" + metric + "_reported"
		if value, ok := attrs[key]; ok {
			add(key, value.AsString())
		}
	}
	if span.Status().Code == codes.Error {
		safe.status.Description = "Builder operation failed"
		add("langfuse.observation.level", "ERROR")
		add("langfuse.observation.status_message", safe.status.Description)
	}
	if o.capture {
		for source, target := range map[string]string{"genkit:input": "langfuse.observation.input", "genkit:output": "langfuse.observation.output"} {
			if value, ok := attrs[source]; ok {
				add(target, o.content(value.AsString()))
			}
		}
	}
	// Sanitize before the bounded, non-blocking queue, not just at HTTP export.
	o.batch.OnEnd(safe)
}

func (o *observability) ForceFlush(ctx context.Context) error { return o.batch.ForceFlush(ctx) }
func (o *observability) Shutdown(ctx context.Context) error   { return o.batch.Shutdown(ctx) }
func (o *observability) close(deadline time.Time) {
	o.once.Do(func() {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		_ = o.Shutdown(ctx)
		o.provider.UnregisterSpanProcessor(o)
	})
}

type safeExporter struct {
	sdktrace.SpanExporter
	log *slog.Logger
}

func (e *safeExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := e.SpanExporter.ExportSpans(ctx, spans); err != nil {
		e.log.Warn("Langfuse export unavailable; Builder continues")
	}
	// Do not send upstream errors/response bodies to OTel's global error logger.
	return nil
}
func (e *safeExporter) Shutdown(ctx context.Context) error {
	_ = e.SpanExporter.Shutdown(ctx)
	return nil
}

// Raw events/status, resource labels, links and trace state may contain upstream
// descriptions, credentials or unrelated tenant data. Export only owned fields.
type exportSpan struct {
	sdktrace.ReadOnlySpan
	attrs  []attribute.KeyValue
	name   string
	status sdktrace.Status
}

func (s *exportSpan) Attributes() []attribute.KeyValue { return s.attrs }
func (s *exportSpan) Name() string                     { return s.name }
func (s *exportSpan) Status() sdktrace.Status          { return s.status }
func (s *exportSpan) Events() []sdktrace.Event         { return nil }
func (s *exportSpan) Links() []sdktrace.Link           { return nil }
func (s *exportSpan) Resource() *resource.Resource {
	return resource.NewSchemaless(attribute.String("service.name", "piko"))
}
func (s *exportSpan) InstrumentationScope() instrumentation.Scope {
	return instrumentation.Scope{Name: "piko.builder"}
}
func (s *exportSpan) InstrumentationLibrary() instrumentation.Scope {
	return s.InstrumentationScope()
}
func (s *exportSpan) SpanContext() trace.SpanContext {
	return s.ReadOnlySpan.SpanContext().WithTraceState(trace.TraceState{})
}
func (s *exportSpan) Parent() trace.SpanContext {
	return s.ReadOnlySpan.Parent().WithTraceState(trace.TraceState{})
}

// Consume complete quoted values first, including whitespace and escaped
// quotes. The unquoted fallback alone would redact only a password's first word.
var quotedCredential = regexp.MustCompile(`(?i)[a-z0-9_]*(?:password|secret|token|api_key|authorization|cookie)[a-z0-9_]*["']?\s*[:=]\s*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')`)
var credentialText = regexp.MustCompile(`(?i)(?:https?://[^\s/@"\\]+:[^\s/@"\\]+@|bearer\s+[^\s"\\]+|\b[0-9]{5,}:[A-Za-z0-9_-]{20,}|\b(?:sk|pk)-[A-Za-z0-9_-]+|(?:[a-z0-9_]*(?:password|secret|token|api_key|authorization|cookie)[a-z0-9_]*)["']?\s*[:=]\s*["']?[^\s,;"'\\}]+|\b[A-Za-z0-9_+/=-]{32,}\b)`)
var credentialKey = regexp.MustCompile(`(?i)password|secret|token|api.?key|authorization|cookie|credential|encryption`)

func (o *observability) clean(value string) string {
	for _, secret := range o.secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = quotedCredential.ReplaceAllString(value, "[REDACTED]")
	return credentialText.ReplaceAllString(value, "[REDACTED]")
}
func (o *observability) content(value string) string {
	// Genkit captures complete schemas/options. Bound each field and fail closed
	// for invalid/oversized JSON rather than exporting an uninspected raw value.
	if len(value) > 128<<10 {
		return `"[OMITTED: content limit]"`
	}
	var decoded any
	if json.Unmarshal([]byte(value), &decoded) != nil {
		return `"[OMITTED]"`
	}
	var clean func(any) any
	clean = func(v any) any {
		switch x := v.(type) {
		case string:
			return o.clean(x)
		case []any:
			for i := range x {
				x[i] = clean(x[i])
			}
			return x
		case map[string]any:
			result := make(map[string]any, len(x))
			for k, entry := range x {
				key := o.clean(k)
				if credentialKey.MatchString(k) {
					result[key] = "[REDACTED]"
				} else {
					result[key] = clean(entry)
				}
			}
			return result
		default:
			return v
		}
	}
	encoded, err := json.Marshal(clean(decoded))
	if err != nil {
		return `"[OMITTED]"`
	}
	return string(encoded)
}
