package app

import (
	"testing"
	"time"
)

func TestBuilderEnvironmentConfigurationAndLimits(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_PATH", "test.db")
	t.Setenv("SESSION_SECRET", "a-long-test-secret-with-at-least-32-bytes")
	t.Setenv("BOT_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	for _, key := range []string{"OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENROUTER_MODEL", "BUILDER_DAILY_REQUESTS", "BUILDER_MAX_CALLS", "BUILDER_RUN_TIMEOUT"} {
		t.Setenv(key, "")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Builder.Model != "openai/gpt-6-luna" || cfg.Builder.DailyRequests != 200 || cfg.Builder.MaxCalls != 20 || cfg.Builder.RunTimeout != 8*time.Minute || cfg.Builder.APIKey != "" {
		t.Fatal("Builder defaults changed")
	}
	t.Setenv("OPENROUTER_API_KEY", "server-key")
	t.Setenv("OPENROUTER_BASE_URL", "http://localhost:1234/v1")
	t.Setenv("OPENROUTER_MODEL", "vendor/model")
	t.Setenv("BUILDER_DAILY_REQUESTS", "7")
	t.Setenv("BUILDER_MAX_CALLS", "2")
	t.Setenv("BUILDER_RUN_TIMEOUT", "3s")
	cfg, err = LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Builder.APIKey != "server-key" || cfg.Builder.BaseURL != "http://localhost:1234/v1" || cfg.Builder.Model != "vendor/model" || cfg.Builder.DailyRequests != 7 || cfg.Builder.MaxCalls != 2 || cfg.Builder.RunTimeout != 3*time.Second {
		t.Fatal("Builder environment overrides ignored")
	}
	for _, tc := range []struct{ key, value string }{{"BUILDER_DAILY_REQUESTS", "0"}, {"BUILDER_DAILY_REQUESTS", "-1"}, {"BUILDER_MAX_CALLS", "oops"}, {"BUILDER_MAX_CALLS", "0"}, {"BUILDER_RUN_TIMEOUT", "0s"}, {"BUILDER_RUN_TIMEOUT", "-1s"}, {"OPENROUTER_BASE_URL", "https://user:secret@provider.test/v1"}, {"OPENROUTER_MODEL", "\n"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := LoadConfig(); err == nil {
				t.Fatal("invalid Builder config accepted")
			}
		})
	}
}

func TestConfigRejectsShortSecret(t *testing.T) {
	t.Setenv("DATABASE_PATH", "test.db")
	t.Setenv("SESSION_SECRET", "short")
	if _, e := LoadConfig(); e == nil {
		t.Fatal("short secret accepted")
	}
}

func TestProductionRequiresSecureSessionCookie(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_PATH", "test.db")
	t.Setenv("SESSION_SECRET", "a-long-test-secret-with-at-least-32-bytes")
	t.Setenv("COOKIE_SECURE", "false")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("insecure production session cookie accepted")
	}
}

func TestProductionRequiresPublicHTTPSBotEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "http://bots.example.test", "https://user:secret@bots.example.test", "https://bots.example.test/path", "https://bots.example.test?secret=hidden", "https://bots.example.test#hidden", "https://bots.example.test:8080", "https://localhost", "https://127.0.0.1", "https://192.168.1.2"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("APP_ENV", "production")
			t.Setenv("DATABASE_PATH", "test.db")
			t.Setenv("SESSION_SECRET", "a-long-test-secret-with-at-least-32-bytes")
			t.Setenv("COOKIE_SECURE", "true")
			t.Setenv("BOT_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
			t.Setenv("BOT_PUBLIC_URL", endpoint)
			if _, err := LoadConfig(); err == nil {
				t.Fatal("invalid production endpoint accepted")
			}
		})
	}
}
