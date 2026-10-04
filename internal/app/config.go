package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/builder"
)

type Config struct {
	Environment      string
	HTTPAddr         string
	DatabasePath     string
	SessionSecret    string
	BotEncryptionKey string
	BotPublicURL     string
	LogLevel         string
	TrustedProxy     bool
	CookieSecure     bool
	ShutdownPeriod   time.Duration
	Builder          builder.Config
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Environment:      env("APP_ENV", "development"),
		HTTPAddr:         env("HTTP_ADDR", ":8080"),
		DatabasePath:     os.Getenv("DATABASE_PATH"),
		SessionSecret:    os.Getenv("SESSION_SECRET"),
		BotEncryptionKey: os.Getenv("BOT_ENCRYPTION_KEY"),
		BotPublicURL:     os.Getenv("BOT_PUBLIC_URL"),
		LogLevel:         env("LOG_LEVEL", "info"),
		ShutdownPeriod:   10 * time.Second,
	}
	var err error
	cfg.Builder = builder.Config{APIKey: os.Getenv("OPENROUTER_API_KEY"), BaseURL: env("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"), Model: env("OPENROUTER_MODEL", "openai/gpt-6-luna"), DailyRequests: 200, MaxCalls: 20, RunTimeout: 8 * time.Minute}
	// Only the explicit literal true enables content. Invalid/absent values
	// fail closed without making optional monitoring an application dependency.
	cfg.Builder.Langfuse = builder.TraceConfig{BaseURL: os.Getenv("LANGFUSE_BASE_URL"), PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"), SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"), CaptureContent: os.Getenv("LANGFUSE_CAPTURE_CONTENT") == "true"}
	for _, setting := range []struct {
		key    string
		target *int64
	}{{"BUILDER_DAILY_REQUESTS", &cfg.Builder.DailyRequests}, {"BUILDER_MAX_CALLS", &cfg.Builder.MaxCalls}} {
		if raw := os.Getenv(setting.key); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || n < 1 {
				return Config{}, fmt.Errorf("%s must be a positive integer", setting.key)
			}
			*setting.target = n
		}
	}
	if raw := os.Getenv("BUILDER_RUN_TIMEOUT"); raw != "" {
		cfg.Builder.RunTimeout, err = time.ParseDuration(raw)
		if err != nil || cfg.Builder.RunTimeout <= 0 {
			return Config{}, errors.New("BUILDER_RUN_TIMEOUT must be a positive duration")
		}
	}
	if err := cfg.Builder.Validate(); err != nil {
		return Config{}, err
	}
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
	if cfg.Environment == "production" && !cfg.CookieSecure {
		return Config{}, errors.New("COOKIE_SECURE must be true in production")
	}
	if _, err := bot.EncryptionKey(cfg.BotEncryptionKey, cfg.SessionSecret); err != nil {
		return Config{}, err
	}
	if err := bot.ValidateDeliveryConfig(cfg.Environment, cfg.BotPublicURL); err != nil {
		return Config{}, err
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
