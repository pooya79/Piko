package app

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	source "github.com/pooya79/Piko/db"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/testsupport"
	"github.com/pooya79/Piko/internal/web"
)

type accountBrowser struct {
	router http.Handler
	jar    *cookiejar.Jar
	base   *url.URL
}

func accountTestRouter(t *testing.T, db *sql.DB) (http.Handler, *auth.Service) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	credentials := auth.NewService(dbgen.New(db))
	accounts := auth.NewAccountService(auth.NewAccountRepository(db), credentials)
	mw := web.Middleware{Auth: credentials, LocaleCatalog: testLocaleCatalog(t), Log: logger, Secret: []byte("test-secret")}
	return buildRouter(db, mw, web.NewRateLimiter(db, logger, mw.ClientIP), auth.NewHandler(credentials, accounts, logger, false)), credentials
}

func newAccountBrowser(t *testing.T, router http.Handler) *accountBrowser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse("http://example.test")
	return &accountBrowser{router: router, jar: jar, base: base}
}

func (b *accountBrowser) cookie(name string) string {
	for _, c := range b.jar.Cookies(b.base) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// Requests traverse the actual router, middleware, and SQLite, with browser cookie semantics.
func (b *accountBrowser) send(method, path string, form url.Values) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range b.jar.Cookies(b.base) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.router.ServeHTTP(w, r)
	b.jar.SetCookies(b.base, w.Result().Cookies())
	return w
}

func (b *accountBrowser) post(path string, form url.Values) *httptest.ResponseRecorder {
	form.Set("csrf_token", b.cookie(auth.CSRFCookie))
	return b.send(http.MethodPost, path, form)
}

func registerValues(email, name, password string) url.Values {
	return url.Values{"email": {email}, "display_name": {name}, "password": {password}}
}

func TestDuplicateRegistrationPreservesAccountAndGrantsNoSession(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	router, credentials := accountTestRouter(t, db)
	original := newAccountBrowser(t, router)
	original.send(http.MethodGet, "/register", nil)
	if got := original.post("/register", registerValues("mina@example.test", "مینا", "Original123")); got.Code != http.StatusSeeOther {
		t.Fatalf("registration status=%d", got.Code)
	}
	existing, err := credentials.Authenticate(t.Context(), "mina@example.test", "Original123")
	if err != nil {
		t.Fatal(err)
	}
	visitor := newAccountBrowser(t, router)
	visitor.send(http.MethodGet, "/register", nil)
	got := visitor.post("/register", registerValues(" MINA@EXAMPLE.TEST ", "Someone else", "Replacement123"))
	nodes := feedbackElements(t, got.Body.String())
	if got.Code != http.StatusUnprocessableEntity || feedbackAttr(nodes["email"], "aria-invalid") != "true" || !strings.Contains(got.Body.String(), "این نشانی ایمیل قبلاً ثبت شده است.") || !strings.Contains(got.Body.String(), `href="/login"`) {
		t.Fatalf("duplicate feedback: status=%d body=%s", got.Code, got.Body.String())
	}
	if visitor.cookie(auth.SessionCookie) != "" {
		t.Fatal("duplicate registration granted a session")
	}
	if got := visitor.send(http.MethodGet, "/dashboard", nil); got.Code != http.StatusSeeOther {
		t.Fatal("duplicate visitor gained access")
	}
	retained, err := credentials.Authenticate(t.Context(), "mina@example.test", "Original123")
	if err != nil || retained.ID != existing.ID || retained.DisplayName != "مینا" {
		t.Fatalf("original account changed: %+v %v", retained, err)
	}
	if _, err := credentials.Authenticate(t.Context(), "mina@example.test", "Replacement123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatal("duplicate changed password")
	}
	if _, err := credentials.LoadSession(t.Context(), original.cookie(auth.SessionCookie)); err != nil {
		t.Fatal("duplicate invalidated original session", err)
	}
}

func TestRegistrationSessionFailureRollsBackAccount(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	router, credentials := accountTestRouter(t, db)
	visitor := newAccountBrowser(t, router)
	visitor.send(http.MethodGet, "/register", nil)
	if _, err := db.Exec("CREATE TRIGGER fail_session BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT, 'forced session failure'); END"); err != nil {
		t.Fatal(err)
	}
	got := visitor.post("/register", registerValues("rollback@example.test", "Mina", "Original123"))
	if got.Code != http.StatusInternalServerError || visitor.cookie(auth.SessionCookie) != "" {
		t.Fatalf("failed registration status=%d", got.Code)
	}
	if _, err := credentials.Authenticate(t.Context(), "rollback@example.test", "Original123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("failed registration retained account: %v", err)
	}
	if _, err := db.Exec("DROP TRIGGER fail_session"); err != nil {
		t.Fatal(err)
	}
	if got := visitor.post("/register", registerValues("rollback@example.test", "Mina", "Original123")); got.Code != http.StatusSeeOther {
		t.Fatalf("rollback did not free email: %d", got.Code)
	}
}

func TestRemovedAccountRoutesAreNotFoundWithValidCSRF(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	router, _ := accountTestRouter(t, db)
	visitor := newAccountBrowser(t, router)
	visitor.send(http.MethodGet, "/login", nil)
	for _, path := range []string{"/language", "/verify", "/verify/pending", "/verify/resend", "/verify/cancel", "/password/forgot", "/password/reset"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			var got *httptest.ResponseRecorder
			if method == http.MethodPost {
				got = visitor.post(path, url.Values{})
			} else {
				got = visitor.send(method, path, nil)
			}
			if got.Code != http.StatusNotFound {
				t.Errorf("%s %s status=%d", method, path, got.Code)
			}
		}
	}
	for _, path := range []string{"/login", "/register", "/account"} {
		got := visitor.send(http.MethodGet, path, nil)
		if strings.Contains(got.Body.String(), "/verify") || strings.Contains(got.Body.String(), "/password/") {
			t.Errorf("%s retains retired links", path)
		}
	}
}

func TestAccountRoutesEnforceCSRFAndSessionExpiration(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	router, credentials := accountTestRouter(t, db)
	visitor := newAccountBrowser(t, router)
	visitor.send(http.MethodGet, "/register", nil)
	for _, path := range []string{"/register", "/login", "/logout"} {
		if got := visitor.send(http.MethodPost, path, registerValues("mina@example.test", "Mina", "Original123")); got.Code != http.StatusForbidden {
			t.Errorf("%s accepted missing CSRF: %d", path, got.Code)
		}
	}
	if got := visitor.post("/register", registerValues("mina@example.test", "Mina", "Original123")); got.Code != http.StatusSeeOther {
		t.Fatalf("registration=%d", got.Code)
	}
	for _, path := range []string{"/register", "/login", "/logout"} {
		if got := visitor.send(http.MethodPost, path, url.Values{"csrf_token": {"wrong"}}); got.Code != http.StatusForbidden {
			t.Errorf("%s accepted invalid session CSRF: %d", path, got.Code)
		}
	}
	sessionCookie := visitor.cookie(auth.SessionCookie)
	if _, err := db.Exec("UPDATE sessions SET expires_at = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.LoadSession(t.Context(), sessionCookie); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("expired session loaded: %v", err)
	}
	for _, path := range []string{"/dashboard", "/account"} {
		if got := visitor.send(http.MethodGet, path, nil); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/login" {
			t.Errorf("%s accepted expired session", path)
		}
	}
}

func TestForwardMigrationPreservesLegacyAccountsAndSessions(t *testing.T) {
	ctx := t.Context()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	baseline, err := source.Files.ReadFile("migrations/000001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(baseline)+"CREATE TABLE schema_migrations(version TEXT PRIMARY KEY); INSERT INTO schema_migrations VALUES ('000001_init');"); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("Original-password123")
	if err != nil {
		t.Fatal(err)
	}
	for _, verified := range []bool{false, true} {
		var status any
		if verified {
			status = time.Now().UnixMilli()
		}
		email := "pending@example.test"
		if verified {
			email = "verified@example.test"
		}
		if _, err := db.Exec("INSERT INTO users(email, display_name, password_hash, email_verified_at, preferred_language) VALUES (?, ?, ?, ?, 'en')", email, "Legacy مینا", hash, status); err != nil {
			t.Fatal(err)
		}
	}
	oldSession := "legacy-session-cookie"
	oldCSRF := "legacy-csrf-token"
	sessionHash, csrfHash := sha256.Sum256([]byte(oldSession)), sha256.Sum256([]byte(oldCSRF))
	expires := time.Now().Add(time.Hour).UnixMilli()
	if _, err := db.Exec("INSERT INTO sessions(token_hash, user_id, csrf_hash, expires_at) VALUES (?, 1, ?, ?)", sessionHash[:], csrfHash[:], expires); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO rate_limits VALUES ('rate:account-email:old', 5, ?), ('rate:login:active', 3, ?)", expires, expires); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Migrate(ctx, db, false); err != nil {
			t.Fatal(err)
		}
	}
	var languageColumns int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('users') WHERE name='preferred_language'").Scan(&languageColumns); err != nil || languageColumns != 0 {
		t.Fatalf("retired language preference remains: %d %v", languageColumns, err)
	}
	q := dbgen.New(db)
	for i, email := range []string{"pending@example.test", "verified@example.test"} {
		saved, err := q.GetUserByEmail(ctx, email)
		if err != nil || saved.ID != int64(i+1) || saved.DisplayName != "Legacy مینا" || saved.PasswordHash != hash {
			t.Fatalf("migration changed legacy account: %+v %v", saved, err)
		}
	}
	router, credentials := accountTestRouter(t, db)
	if loaded, err := credentials.LoadSession(ctx, oldSession); err != nil || loaded.User.ID != 1 || loaded.ExpiresAt.UnixMilli() != expires || !credentials.VerifyCSRF(ctx, oldSession, oldCSRF) {
		t.Fatalf("migration invalidated legacy session: %+v %v", loaded, err)
	}
	visitor := newAccountBrowser(t, router)
	visitor.jar.SetCookies(visitor.base, []*http.Cookie{{Name: auth.SessionCookie, Value: oldSession}, {Name: auth.CSRFCookie, Value: oldCSRF}})
	if got := visitor.send(http.MethodGet, "/dashboard", nil); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Legacy مینا") || !strings.Contains(got.Body.String(), `lang="fa"`) || !strings.Contains(got.Body.String(), `dir="rtl"`) {
		t.Fatalf("legacy session cannot access Dashboard: %d", got.Code)
	}
	visitor.post("/logout", url.Values{})
	for _, email := range []string{"pending@example.test", "verified@example.test"} {
		visitor.send(http.MethodGet, "/login", nil)
		if got := visitor.post("/login", url.Values{"email": {email}, "password": {"Original-password123"}}); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/dashboard" {
			t.Fatalf("legacy login %s: %d", email, got.Code)
		}
		visitor.post("/logout", url.Values{})
	}
	for _, table := range []string{"account_challenges", "signup_receipts", "account_email_requests", "email_jobs"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 0 {
			t.Errorf("retired table %s retained: %v", table, err)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM rate_limits WHERE key='rate:account-email:old'").Scan(&count); err != nil || count != 0 {
		t.Errorf("retired budget retained: %v", err)
	}
	if err := db.QueryRow("SELECT count(*) FROM rate_limits WHERE key='rate:login:active' AND count=3").Scan(&count); err != nil || count != 1 {
		t.Errorf("authentication budget changed: %v", err)
	}
}

func TestAuthenticationRateLimitsRemainEffective(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	router, _ := accountTestRouter(t, db)
	visitor := newAccountBrowser(t, router)
	visitor.send(http.MethodGet, "/register", nil)
	for _, tc := range []struct {
		path  string
		limit int
	}{{"/register", 5}, {"/login", 10}} {
		for range tc.limit {
			if got := visitor.post(tc.path, registerValues("invalid", "Mina", "Original123")); got.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s limited prematurely: %d", tc.path, got.Code)
			}
		}
		if got := visitor.post(tc.path, registerValues("invalid", "Mina", "Original123")); got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
			t.Errorf("%s not limited: %d", tc.path, got.Code)
		}
	}
}

func TestRegistrationPreservesUnicodeNameAndCookieProtections(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	credentials := auth.NewService(dbgen.New(db))
	mw := web.Middleware{Auth: credentials, LocaleCatalog: testLocaleCatalog(t), Log: logger, Secret: []byte("test-secret"), SecureCookie: true}
	router := buildRouter(db, mw, web.NewRateLimiter(db, logger, mw.ClientIP), auth.NewHandler(credentials, auth.NewAccountService(auth.NewAccountRepository(db), credentials), logger, true))
	visitor := newAccountBrowser(t, router)
	visitor.base.Scheme = "https"
	visitor.send(http.MethodGet, "/register", nil)
	name := "مینا <script>alert(1)</script> Mina " + strings.Repeat("م", 44)
	got := visitor.post("/register", registerValues("unicode@example.test", "  "+name+"  ", "abcde1"))
	if got.Code != http.StatusSeeOther {
		t.Fatalf("valid Unicode name or six-character password rejected: %d", got.Code)
	}
	var session, csrf *http.Cookie
	for _, c := range got.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			session = c
		}
		if c.Name == auth.CSRFCookie {
			csrf = c
		}
	}
	if session == nil || !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.MaxAge <= 0 {
		t.Fatalf("session cookie protections=%+v", session)
	}
	if csrf == nil || !csrf.Secure || csrf.HttpOnly || csrf.SameSite != http.SameSiteLaxMode || csrf.MaxAge <= 0 {
		t.Fatalf("CSRF cookie protections=%+v", csrf)
	}
	dashboard := visitor.send(http.MethodGet, "/dashboard", nil)
	for _, path := range []string{"/dashboard", "/account"} {
		page := dashboard
		if path == "/account" {
			page = visitor.send(http.MethodGet, path, nil)
		}
		if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "<script>alert(1)</script>") || !strings.Contains(page.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(page.Body.String(), `<bdi dir="auto">`) {
			t.Errorf("%s did not safely isolate the complete saved Display name", path)
		}
	}
	for _, cookie := range got.Result().Cookies() {
		if cookie.Name == "piko_language" {
			t.Error("registration issued a retired language cookie")
		}
	}
}
