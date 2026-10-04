package builder

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	APIKey        string
	BaseURL       string
	Model         string
	DailyRequests int64
	MaxCalls      int64
	RunTimeout    time.Duration
}

func (c Config) defaults() Config {
	if c.BaseURL == "" {
		c.BaseURL = "https://openrouter.ai/api/v1"
	}
	if c.Model == "" {
		c.Model = "openai/gpt-6-luna"
	}
	if c.DailyRequests == 0 {
		c.DailyRequests = 200
	}
	if c.MaxCalls == 0 {
		c.MaxCalls = 20
	}
	if c.RunTimeout == 0 {
		c.RunTimeout = 8 * time.Minute
	}
	c.APIKey = strings.TrimSpace(c.APIKey)
	return c
}

func (c Config) Validate() error {
	c = c.defaults()
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid OPENROUTER_BASE_URL")
	}
	if strings.TrimSpace(c.Model) == "" || strings.ContainsAny(c.Model, "\r\n") {
		return errors.New("invalid OPENROUTER_MODEL")
	}
	if c.DailyRequests < 1 || c.MaxCalls < 1 || c.RunTimeout <= 0 {
		return errors.New("builder limits must be positive")
	}
	return nil
}
