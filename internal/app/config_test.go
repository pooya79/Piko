package app

import "testing"

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
