package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
)

type revokedSessionService struct{}

func (revokedSessionService) LoadSession(context.Context, string) (auth.Session, error) {
	return auth.Session{}, auth.ErrSessionNotFound
}
func (revokedSessionService) VerifyCSRF(context.Context, string, string) bool { return false }
func (revokedSessionService) RenewCSRF(context.Context, string) (string, error) {
	return "", auth.ErrSessionNotFound
}

func TestRevokedSessionCanOpenAndSubmitLoginForm(t *testing.T) {
	m := Middleware{Auth: revokedSessionService{}, Secret: []byte("test-secret")}
	h := m.Session(m.CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			c, err := r.Cookie(auth.CSRFCookie)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fmt.Fprint(w, c.Value)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))

	get := httptest.NewRequest(http.MethodGet, "/login", nil)
	get.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "revoked"})
	get.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: "old-session-token"})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, get)
	csrf := response.Body.String()
	if csrf == "" || csrf == "old-session-token" {
		t.Fatalf("login form retained stale CSRF token %q", csrf)
	}
	var sessionCleared, csrfSet bool
	for _, c := range response.Result().Cookies() {
		if c.Name == auth.SessionCookie && c.MaxAge < 0 {
			sessionCleared = true
		}
		if c.Name == auth.CSRFCookie && c.Value == csrf {
			csrfSet = true
		}
	}
	if !sessionCleared || !csrfSet {
		t.Fatalf("stale session not cleared or CSRF not refreshed: %v", response.Result().Cookies())
	}

	form := url.Values{"csrf_token": {csrf}}
	post := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: csrf})
	response = httptest.NewRecorder()
	h.ServeHTTP(response, post)
	if response.Code != http.StatusNoContent {
		t.Fatalf("login form rejected after recovery: status=%d", response.Code)
	}
}

type activeSessionService struct{}

func (activeSessionService) LoadSession(context.Context, string) (auth.Session, error) {
	return auth.Session{User: auth.User{ID: 7}}, nil
}
func (activeSessionService) VerifyCSRF(_ context.Context, _, token string) bool {
	return token == "renewed-token"
}
func (activeSessionService) RenewCSRF(context.Context, string) (string, error) {
	return "renewed-token", nil
}

func TestActiveSessionRenewsMissingCSRFToken(t *testing.T) {
	m := Middleware{Auth: activeSessionService{}, Secret: []byte("test-secret")}
	h := m.Session(m.CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			c, err := r.Cookie(auth.CSRFCookie)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fmt.Fprint(w, c.Value)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	get := httptest.NewRequest(http.MethodGet, "/account", nil)
	get.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "active"})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, get)
	if got := response.Body.String(); got != "renewed-token" {
		t.Fatalf("rendered CSRF token=%q", got)
	}

	form := url.Values{"csrf_token": {"renewed-token"}}
	post := httptest.NewRequest(http.MethodPost, "/account", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "active"})
	post.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: "renewed-token"})
	response = httptest.NewRecorder()
	h.ServeHTTP(response, post)
	if response.Code != http.StatusNoContent {
		t.Fatalf("renewed token rejected: status=%d", response.Code)
	}
}
