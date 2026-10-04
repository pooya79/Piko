package builder

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type runContextKey struct{}

// The SDK may perform multiple calls in a generation. Count at the wire boundary,
// before every attempt, and retain accounting even if Genkit rejects the output.
// SDK transport retries are disabled so no upstream calls bypass this boundary.
type accountingTransport struct {
	service *Service
	base    http.RoundTripper
}

func (t *accountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	run, ok := req.Context().Value(runContextKey{}).(admittedRun)
	if !ok {
		return nil, ErrCallLimit
	}
	s := t.service
	seq, err := s.startCall(req.Context(), run)
	if err != nil {
		return nil, err
	}
	recordTraceUsage(req.Context(), seq, Usage{})
	response, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		response.Body = &accountingStream{ReadCloser: response.Body, service: s, run: run, sequence: seq, ctx: req.Context()}
		return response, nil
	}
	// Bound non-streamed provider error data before parsing.
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	_ = response.Body.Close()
	if readErr != nil {
		return nil, errors.New("builder provider response unreadable")
	}
	if len(body) > 2<<20 {
		return nil, errors.New("builder provider response too large")
	}
	u := reportedUsage(body)
	recordTraceUsage(req.Context(), seq, u)
	if err := s.accountCall(run, seq, u); err != nil {
		return nil, errors.New("builder accounting unavailable")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func reportedUsage(body []byte) Usage {
	var response struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil {
		return Usage{}
	}
	tokens := func(key string) sql.NullInt64 {
		var n int64
		raw, ok := response.Usage[key]
		if !ok || string(raw) == "null" || json.Unmarshal(raw, &n) != nil || n < 0 {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: n, Valid: true}
	}
	u := Usage{Input: tokens("prompt_tokens"), Output: tokens("completion_tokens"), Total: tokens("total_tokens")}
	if raw, ok := response.Usage["cost"]; ok {
		var number json.Number
		if len(raw) > 0 && raw[0] != '"' && json.Unmarshal(raw, &number) == nil && len(number.String()) <= 64 {
			// Bound the exponent before formatting: a tiny JSON number such as
			// 1e1000000 otherwise expands into megabytes of decimal text.
			if cost, err := decimal.NewFromString(number.String()); err == nil && !cost.IsNegative() && cost.Exponent() >= -18 && cost.Exponent() <= 18 {
				u.Cost = sql.NullString{String: cost.String(), Valid: true}
			}
		}
	}
	return u
}

// Inspect SSE usage as it crosses the wire, before the SDK can reject a chunk.
// Keep reported metrics even when a later provider error or Stop ends the call.
type accountingStream struct {
	io.ReadCloser
	service  *Service
	run      admittedRun
	sequence int64
	pending  []byte
	bytes    int
	usage    Usage
	ctx      context.Context
}

func (s *accountingStream) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	s.bytes += n
	if s.bytes > 2<<20 {
		return 0, errors.New("builder provider response too large")
	}
	s.pending = append(s.pending, p[:n]...)
	for {
		i := bytes.IndexByte(s.pending, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(s.pending[:i])
		s.pending = s.pending[i+1:]
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		usage := reportedUsage(bytes.TrimSpace(line[5:]))
		if !usage.Input.Valid && !usage.Output.Valid && !usage.Total.Valid && !usage.Cost.Valid {
			continue
		}
		if usage.Input.Valid {
			s.usage.Input = usage.Input
		}
		if usage.Output.Valid {
			s.usage.Output = usage.Output
		}
		if usage.Total.Valid {
			s.usage.Total = usage.Total
		}
		if usage.Cost.Valid {
			s.usage.Cost = usage.Cost
		}
		if e := s.service.accountCall(s.run, s.sequence, s.usage); e != nil {
			return 0, errors.New("builder accounting unavailable")
		}
		recordTraceUsage(s.ctx, s.sequence, s.usage)
	}
	return n, err
}

// Record only provider-reported metrics on the active native Genkit model span.
// SDK-generated defaults cannot turn missing accounting into reported zero.
func recordTraceUsage(ctx context.Context, sequence int64, usage Usage) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("piko.call.sequence", strconv.FormatInt(sequence, 10)))
	for _, metric := range []struct {
		name     string
		reported bool
	}{
		{"input_tokens", usage.Input.Valid}, {"output_tokens", usage.Output.Valid}, {"total_tokens", usage.Total.Valid}, {"cost", usage.Cost.Valid},
	} {
		span.SetAttributes(attribute.String("langfuse.observation.metadata.provider_"+metric.name+"_reported", strconv.FormatBool(metric.reported)))
	}
	tokens := map[string]int64{}
	if usage.Input.Valid {
		tokens["input"] = usage.Input.Int64
	}
	if usage.Output.Valid {
		tokens["output"] = usage.Output.Int64
	}
	if usage.Total.Valid {
		tokens["total"] = usage.Total.Int64
	}
	if len(tokens) > 0 {
		encoded, _ := json.Marshal(tokens)
		span.SetAttributes(attribute.String("langfuse.observation.usage_details", string(encoded)))
	}
	if usage.Cost.Valid {
		span.SetAttributes(attribute.String("langfuse.observation.cost_details", `{"total":`+usage.Cost.String+`}`))
	}
}
