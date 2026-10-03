package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/testsupport"
	"github.com/pooya79/Piko/internal/web"
)

// Exercise the public routes with real SQLite storage and cookie/CSRF rotation.
func TestAuthJourneyAgainstSQLite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, _ := testsupport.MigratedSQLite(t, ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := testLocaleCatalog(t)
	credentials := auth.NewService(dbgen.New(pool))
	accounts := auth.NewAccountService(auth.NewAccountRepository(pool), credentials)
	mw := web.Middleware{Auth: credentials, LocaleCatalog: catalog, Log: logger, Secret: []byte("test-csrf-secret")}
	rateKey := fmt.Sprintf("auth-journey-%d", time.Now().UnixNano())
	limiter := web.NewRateLimiter(pool, logger, func(*http.Request) string { return rateKey })
	server := httptest.NewServer(buildRouter(pool, mw, limiter, auth.NewHandler(credentials, accounts, logger, false)))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base, _ := url.Parse(server.URL)
	jar.SetCookies(base, []*http.Cookie{{Name: "piko_language", Value: "en"}})
	cookie := func(name string) string {
		for _, c := range jar.Cookies(base) {
			if c.Name == name {
				return c.Value
			}
		}
		return ""
	}
	send := func(method, path string, form url.Values, status int, location string) string {
		t.Helper()
		var response *http.Response
		var err error
		if method == http.MethodGet {
			response, err = client.Get(server.URL + path)
		} else {
			form.Set("csrf_token", cookie(auth.CSRFCookie))
			response, err = client.PostForm(server.URL+path, form)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != status || response.Header.Get("Location") != location {
			t.Fatalf("%s %s: status=%d location=%q body=%s", method, path, response.StatusCode, response.Header.Get("Location"), body)
		}
		return string(body)
	}
	send(http.MethodGet, "/account", nil, http.StatusSeeOther, "/login")
	send(http.MethodGet, "/dashboard", nil, http.StatusSeeOther, "/login")
	send(http.MethodGet, "/", nil, http.StatusFound, "/login")
	send(http.MethodGet, "/register", nil, http.StatusOK, "")
	registration := url.Values{"email": {" Journey@Example.Test "}, "display_name": {"  Mina مینا  "}, "password": {"original-password-123"}}
	send(http.MethodPost, "/register", registration, http.StatusSeeOther, "/dashboard")
	oldSession := cookie(auth.SessionCookie)
	if oldSession == "" {
		t.Fatal("registration did not establish a session")
	}
	dashboard := send(http.MethodGet, "/dashboard", nil, http.StatusOK, "")
	if !strings.Contains(dashboard, "Mina مینا") {
		t.Fatal("saved Display name missing from Dashboard")
	}
	account := send(http.MethodGet, "/account", nil, http.StatusOK, "")
	if !strings.Contains(account, "Mina مینا") || !strings.Contains(account, "journey@example.test") {
		t.Fatal("saved account details missing")
	}
	send(http.MethodGet, "/", nil, http.StatusFound, "/dashboard")
	send(http.MethodPost, "/logout", url.Values{}, http.StatusSeeOther, "/login")
	if _, err := credentials.LoadSession(ctx, oldSession); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("logout retained session: %v", err)
	}
	send(http.MethodGet, "/dashboard", nil, http.StatusSeeOther, "/login")
	send(http.MethodGet, "/login", nil, http.StatusOK, "")
	send(http.MethodPost, "/login", url.Values{"email": {"journey@example.test"}, "password": {"original-password-123"}}, http.StatusSeeOther, "/dashboard")
	send(http.MethodGet, "/dashboard", nil, http.StatusOK, "")
}
