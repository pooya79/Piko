package builder

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

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
	// This slice requests non-streaming replies. Bound provider data before parsing.
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
