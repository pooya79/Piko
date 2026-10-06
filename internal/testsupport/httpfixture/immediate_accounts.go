package httpfixture

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/web"
)

type Browser struct {
	Router http.Handler
	Jar    *cookiejar.Jar
	Base   *url.URL
}

func AccountTestRouter(t *testing.T, db *sql.DB) (http.Handler, *auth.Service) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	credentials := auth.NewService(dbgen.New(db))
	accounts := auth.NewAccountService(auth.NewAccountRepository(db), credentials)
	mw := web.Middleware{Auth: credentials, LocaleCatalog: TestLocaleCatalog(t), Log: logger, Secret: []byte("test-secret")}
	return httpapp.Router(db, mw, web.NewRateLimiter(db, logger, mw.ClientIP), auth.NewHandler(credentials, accounts, logger, false), TestBotService(t, db), nil), credentials
}

func NewAccountBrowser(t *testing.T, router http.Handler) *Browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("http://example.test")
	return &Browser{Router: router, Jar: jar, Base: base}
}

func (b *Browser) Cookie(name string) string {
	for _, c := range b.Jar.Cookies(b.Base) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// Requests traverse the actual router, middleware, and SQLite, with browser cookie semantics.
func (b *Browser) Send(method, path string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range b.Jar.Cookies(b.Base) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.Router.ServeHTTP(w, r)
	b.Jar.SetCookies(b.Base, w.Result().Cookies())
	return w
}

func (b *Browser) Post(path string, form url.Values) *httptest.ResponseRecorder {
	form.Set("csrf_token", b.Cookie(auth.CSRFCookie))
	return b.Send(http.MethodPost, path, form)
}

func RegisterValues(email, name, password string) url.Values {
	return url.Values{"email": {email}, "display_name": {name}, "password": {password}}
}
