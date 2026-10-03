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
