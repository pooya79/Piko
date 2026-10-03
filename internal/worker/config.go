package worker

import (
	"errors"
	"net/url"
	"os"
	"strconv"
)

type Config struct {
	Environment    string
	SessionSecret  string
	PublicBaseURL  string
	SMTPAddress    string
	SMTPFrom       string
	SMTPUsername   string
	SMTPPassword   string
	SMTPRequireTLS bool
	DatabasePath   string
	LogLevel       string
}

func LoadConfig() (Config, error) {
	cfg := Config{
		DatabasePath:  os.Getenv("DATABASE_PATH"),
		Environment:   env("APP_ENV", "development"),
		SessionSecret: os.Getenv("SESSION_SECRET"),
		PublicBaseURL: env("PUBLIC_BASE_URL", "http://localhost:8080"),
		SMTPAddress:   env("SMTP_ADDR", "localhost:1025"),
		SMTPFrom:      env("SMTP_FROM", "buildx@localhost.test"),
		SMTPUsername:  os.Getenv("SMTP_USERNAME"),
		SMTPPassword:  os.Getenv("SMTP_PASSWORD"),
		LogLevel:      env("LOG_LEVEL", "info"),
	}
	tlsValue := env("SMTP_REQUIRE_TLS", strconv.FormatBool(cfg.Environment == "production"))
	var err error
	cfg.SMTPRequireTLS, err = strconv.ParseBool(tlsValue)
	if err != nil {
		return Config{}, errors.New("SMTP_REQUIRE_TLS must be a boolean")
	}
	if len(cfg.SessionSecret) < 32 {
		return Config{}, errors.New("SESSION_SECRET must be at least 32 characters")
	}
	base, err := url.Parse(cfg.PublicBaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.RawQuery != "" || base.Fragment != "" {
		return Config{}, errors.New("PUBLIC_BASE_URL must be an absolute HTTP(S) origin")
	}
	if cfg.Environment == "production" && (base.Scheme != "https" || !cfg.SMTPRequireTLS) {
		return Config{}, errors.New("production requires HTTPS public links and SMTP STARTTLS")
	}
	if cfg.DatabasePath == "" {
		return Config{}, errors.New("DATABASE_PATH is required")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
