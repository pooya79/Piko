package app

import (
	"context"
	"database/sql"
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

type accountEmailQueue struct{ challenges []string }

func (q *accountEmailQueue) EnqueueChallenge(_ context.Context, _ *sql.Tx, nonce string) error {
	q.challenges = append(q.challenges, nonce)
	return nil
}

// Exercise the public routes with real SQLite storage and cookie/CSRF rotation.
func TestAuthJourneyAgainstSQLite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, _ := testsupport.MigratedSQLite(t, ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := testLocaleCatalog(t)
	credentials := auth.NewService(dbgen.New(pool))
	queue := &accountEmailQueue{}
	accounts := auth.NewAccountService(auth.NewAccountRepository(pool, queue), credentials,
		[]byte("test-account-secret-with-at-least-32-bytes"), "http://localhost:8080", catalog)
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
	linkToken := func(index int) string {
		t.Helper()
		message, err := accounts.MessageForChallenge(ctx, queue.challenges[index])
		if err != nil {
			t.Fatal(err)
		}
		link, err := url.Parse(strings.Fields(message.Body)[0])
		if err != nil {
			t.Fatal(err)
		}
		return link.Query().Get("token")
	}
	send(http.MethodGet, "/account", nil, http.StatusSeeOther, "/login")
	send(http.MethodGet, "/register", nil, http.StatusOK, "")
	registration := url.Values{"email": {"journey@example.test"}, "display_name": {"Mina"}, "password": {"original-password-123"}}
	send(http.MethodPost, "/register", registration, http.StatusOK, "")
	if len(queue.challenges) != 1 || cookie(auth.SignupCookie) == "" {
		t.Fatal("registration did not queue verification and issue browser proof")
	}
	verification := linkToken(0)
	send(http.MethodPost, "/verify", url.Values{"token": {verification}}, http.StatusSeeOther, "/account")
	oldSession := cookie(auth.SessionCookie)
	if oldSession == "" {
		t.Fatal("verification did not establish a session")
	}
	account := send(http.MethodGet, "/account", nil, http.StatusOK, "")
	if !strings.Contains(account, "Mina") || !strings.Contains(account, "journey@example.test") {
		t.Fatal("signed-in account details are missing")
	}
	for _, path := range []string{"/dashboard", "/dashboard/portfolios", "/dashboard/markets", "/admin"} {
		send(http.MethodGet, path, nil, http.StatusNotFound, "")
	}
	send(http.MethodPost, "/password/forgot", url.Values{"email": {"journey@example.test"}}, http.StatusOK, "")
	if len(queue.challenges) != 2 {
		t.Fatal("recovery did not queue an email")
	}
	reset := linkToken(1)
	send(http.MethodPost, "/password/reset", url.Values{"token": {reset}, "password": {"replacement-password-123"}}, http.StatusSeeOther, "/login")
	if _, err := credentials.LoadSession(ctx, oldSession); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("reset retained old session: %v", err)
	}
	send(http.MethodGet, "/login", nil, http.StatusOK, "")
	send(http.MethodPost, "/login", url.Values{"email": {"journey@example.test"}, "password": {"original-password-123"}}, http.StatusUnprocessableEntity, "")
	send(http.MethodPost, "/login", url.Values{"email": {"journey@example.test"}, "password": {"replacement-password-123"}}, http.StatusSeeOther, "/account")
	send(http.MethodGet, "/account", nil, http.StatusOK, "")
	send(http.MethodPost, "/password/reset", url.Values{"token": {reset}, "password": {"another-password-123"}}, http.StatusUnprocessableEntity, "")
	send(http.MethodPost, "/logout", url.Values{}, http.StatusSeeOther, "/login")
	send(http.MethodGet, "/account", nil, http.StatusSeeOther, "/login")
}
