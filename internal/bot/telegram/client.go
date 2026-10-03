// Package telegram adapts the Bot API without exposing credential-bearing errors.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	ErrCredentials = errors.New("invalid Telegram credentials")
	ErrUnavailable = errors.New("telegram verification unavailable")
	tokenFormat    = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{20,128}$`)
	usernameFormat = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)
)

type Client struct {
	baseURL string
	http    *http.Client
}
type Identity struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Name     string `json:"first_name"`
	Username string `json:"username"`
}
type Delivery struct {
	HasWebhook     bool
	PendingUpdates int64
}

// NewClient accepts a test server at composition; production always uses Telegram.
// Redirects are refused so a token can never be forwarded to a different origin.
func NewClient(baseURL string, client *http.Client) *Client {
	c := *client
	c.Timeout = 10 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: &c}
}

func (c *Client) Verify(ctx context.Context, token string) (Identity, Delivery, error) {
	if !tokenFormat.MatchString(token) {
		return Identity{}, Delivery{}, ErrCredentials
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var identity Identity
	if err := c.call(ctx, token, "getMe", &identity); err != nil {
		return Identity{}, Delivery{}, err
	}
	if !identity.IsBot || identity.ID <= 0 || strings.TrimSpace(identity.Name) == "" || len(identity.Name) > 256 || !usernameFormat.MatchString(identity.Username) {
		return Identity{}, Delivery{}, ErrUnavailable
	}
	var webhook struct {
		URL            *string `json:"url"`
		PendingUpdates *int64  `json:"pending_update_count"`
	}
	if err := c.call(ctx, token, "getWebhookInfo", &webhook); err != nil {
		return Identity{}, Delivery{}, err
	}
	if webhook.URL == nil || webhook.PendingUpdates == nil || *webhook.PendingUpdates < 0 {
		return Identity{}, Delivery{}, ErrUnavailable
	}
	// Webhook URLs can contain another service's secret. Persist only the conflict.
	return identity, Delivery{HasWebhook: *webhook.URL != "", PendingUpdates: *webhook.PendingUpdates}, nil
}

func (c *Client) call(ctx context.Context, token, method string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+token+"/"+method, nil)
	if err != nil {
		return ErrUnavailable
	}
	response, err := c.http.Do(req)
	// net/http errors include the token in the URL. Never propagate them.
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusNotFound {
		return ErrCredentials
	}
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	var envelope struct {
		OK     bool            `json:"ok"`
		Code   int             `json:"error_code"`
		Result json.RawMessage `json:"result"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &envelope) != nil {
		return ErrUnavailable
	}
	if !envelope.OK {
		if envelope.Code == 401 || envelope.Code == 404 {
			return ErrCredentials
		}
		return ErrUnavailable
	}
	if json.Unmarshal(envelope.Result, result) != nil {
		return ErrUnavailable
	}
	return nil
}
