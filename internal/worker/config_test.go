package worker

import "testing"

func TestConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("missing DATABASE_URL accepted")
	}
}

func TestConfigDoesNotRequireHTTPOrRedisSettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("REDIS_URL", "")
	t.Setenv("SESSION_SECRET", "a-long-test-secret-with-at-least-32-bytes")
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
}

func TestProductionRequiresHTTPSAndSMTPEncryption(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("SESSION_SECRET", "a-long-test-secret-with-at-least-32-bytes")
	for _, tc := range []struct{ origin, tls string }{
		{"http://example.test", "true"}, {"https://example.test", "false"},
	} {
		t.Setenv("PUBLIC_BASE_URL", tc.origin)
		t.Setenv("SMTP_REQUIRE_TLS", tc.tls)
		if _, err := LoadConfig(); err == nil {
			t.Fatal("insecure production email configuration accepted")
		}
	}
}
