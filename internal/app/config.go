package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment    string
	HTTPAddr       string
	DatabasePath   string
	SessionSecret  string
	PublicBaseURL  string
	LogLevel       string
	TrustedProxy   bool
	CookieSecure   bool
	ShutdownPeriod time.Duration
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Environment:    env("APP_ENV", "development"),
		HTTPAddr:       env("HTTP_ADDR", ":8080"),
		DatabasePath:   os.Getenv("DATABASE_PATH"),
		SessionSecret:  os.Getenv("SESSION_SECRET"),
		PublicBaseURL:  env("PUBLIC_BASE_URL", "http://localhost:8080"),
		LogLevel:       env("LOG_LEVEL", "info"),
		ShutdownPeriod: 10 * time.Second,
	}
	var err error
	if cfg.TrustedProxy, err = boolEnv("TRUSTED_PROXY", false); err != nil {
		return Config{}, err
	}
	if cfg.CookieSecure, err = boolEnv("COOKIE_SECURE", cfg.Environment == "production"); err != nil {
		return Config{}, err
	}
	if cfg.DatabasePath == "" {
		return Config{}, errors.New("DATABASE_PATH is required")
	}
	if len(cfg.SessionSecret) < 32 {
		return Config{}, errors.New("SESSION_SECRET must be at least 32 characters")
	}
	base, err := url.Parse(cfg.PublicBaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.RawQuery != "" || base.Fragment != "" {
		return Config{}, errors.New("PUBLIC_BASE_URL must be an absolute HTTP(S) origin")
	}
	if cfg.Environment == "production" && base.Scheme != "https" {
		return Config{}, errors.New("PUBLIC_BASE_URL must use HTTPS in production")
	}
	if cfg.Environment == "production" && !cfg.CookieSecure {
		return Config{}, errors.New("COOKIE_SECURE must be true in production")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func boolEnv(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}
	return b, nil
}
