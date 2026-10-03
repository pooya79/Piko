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
	registration := url.Values{"email": {" Journey@Example.Test "}, "display_name": {"  Mina مینا <نام>  "}, "password": {"original-password-123"}}
	send(http.MethodPost, "/register", registration, http.StatusSeeOther, "/dashboard")
	oldSession := cookie(auth.SessionCookie)
	if oldSession == "" {
		t.Fatal("registration did not establish a session")
	}
	dashboard := send(http.MethodGet, "/dashboard", nil, http.StatusOK, "")
	if !strings.Contains(dashboard, "Mina مینا") || !strings.Contains(dashboard, `lang="fa"`) || !strings.Contains(dashboard, `dir="rtl"`) {
		t.Fatal("saved Display name missing from Dashboard")
	}
	assertEmptyDashboard(t, dashboard, oldSession)
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

// These are product claims at the existing HTTP seam, not a snapshot of the layout.
func assertEmptyDashboard(t *testing.T, body, session string) {
	t.Helper()
	for _, text := range []string{
		"هنوز رباتی نساخته\u200cای", "رباتی برای پایش وجود ندارد",
		"داده\u200cای برای نمایش آمار نداریم", "هنوز گفت\u200cوگویی ثبت نشده",
		"هنوز فعالیتی ثبت نشده", "اعلانی نداری", "به\u200cزودی",
		"اولین رباتت", "گفت\u200cوگوها", "کاربران جدید", "درخواست\u200cهای موفق", "ربات\u200cهای فعال",
		`/static/brand/piko-companion.webp`, `href="/account"`, `action="/logout"`,
	} {
		if !strings.Contains(body, text) {
			t.Errorf("Dashboard missing %q", text)
		}
	}
	if strings.Count(body, "Mina مینا &lt;نام&gt;") < 2 {
		t.Error("saved Display name must identify both greeting and account")
	}
	for _, forbidden := range []string{
		"<نام>", "نازنین", "کافه لیمو", "فروشگاه ماهور", "limoo_cafe_bot", "mahoor_shop_bot",
		"همه\u200cچیز روبه\u200cراهه", "بدون مشکل", "اشتراک حرفه\u200cای", "٪", "sparkline", "chart.js",
		`href="/bots"`, `href="/chat"`, `href="/analytics"`, `href="/billing"`,
		`name="token"`, `name="prompt"`, "journey@example.test", "original-password-123", session,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("Dashboard contains demo content, unavailable destination, or private credential: %q", forbidden)
		}
	}
}
