package worker

import "testing"

func TestConfigRequiresDatabasePath(t *testing.T) {
	t.Setenv("DATABASE_PATH", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("missing DATABASE_PATH accepted")
	}
}

func TestConfigLoadsWorkerSettings(t *testing.T) {
	t.Setenv("DATABASE_PATH", "test.db")
	t.Setenv("SESSION_SECRET", "a-long-test-secret-with-at-least-32-bytes")
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
}

func TestProductionRequiresHTTPSAndSMTPEncryption(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_PATH", "test.db")
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
