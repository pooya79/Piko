package builder

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"
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
	response, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		response.Body = &accountingStream{ReadCloser: response.Body, service: s, run: run, sequence: seq}
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
	}
	return n, err
}
