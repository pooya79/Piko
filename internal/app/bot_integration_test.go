package app

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/testsupport"
)

const testBotToken = "123456:abcdefghijklmnopqrstuvwxyz0123456789"

func testBotService(t *testing.T, db *sql.DB) *bot.Service {
	t.Helper()
	s, err := bot.NewService(bot.NewRepository(db), telegram.NewClient("https://api.telegram.org", http.DefaultClient), []byte("0123456789abcdef0123456789abcdef"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBotMigrationPreservesExistingAccountAndSession(t *testing.T) {
	db, path := testsupport.MigratedSQLite(t, t.Context())
	// Roll back later migrations in this disposable file to represent
	// an installed pre-Bot schema; registration still uses its existing HTTP seam.
	rollbackToMigration(t, db, "000003_persian_only")
	router, _ := accountTestRouter(t, db)
	b := newAccountBrowser(t, router)
	b.send(http.MethodGet, "/register", nil)
	if got := b.post("/register", registerValues("existing@example.test", "نام پیشین", "UnchangedPassword123")); got.Code != 303 {
		t.Fatalf("old schema registration: %d", got.Code)
	}
	if err := database.Migrate(t.Context(), db, false); err != nil {
		t.Fatal(err)
	}
	a, err := New(t.Context(), Config{DatabasePath: path, SessionSecret: "unchanged-session-secret-at-least-32", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", LogLevel: "error"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	b.router = a.server.Handler
	account := b.send(http.MethodGet, "/account", nil)
	if account.Code != 200 || !strings.Contains(account.Body.String(), "نام پیشین") || !strings.Contains(account.Body.String(), "existing@example.test") {
		t.Fatal("forward migration lost account or session")
	}
	b.post("/logout", url.Values{})
	b.send(http.MethodGet, "/login", nil)
	if got := b.post("/login", url.Values{"email": {"existing@example.test"}, "password": {"UnchangedPassword123"}}); got.Code != 303 {
		t.Fatalf("migration changed password: %d", got.Code)
	}
	if got := b.send(http.MethodGet, "/bots", nil); got.Code != 200 {
		t.Fatal("migrated owner cannot access Bots")
	}
}

func TestSlowTelegramVerificationDoesNotLockAccountWrites(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			close(started)
			<-release
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Slow Bot","username":"slow_bot"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		}
	})
	t.Cleanup(func() { close(release) })
	b.send(http.MethodGet, "/bots/connect", nil)
	connected := make(chan int, 1)
	go func() { connected <- b.post("/bots/connect", url.Values{"token": {testBotToken}}).Code }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("verification never started")
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send(http.MethodGet, "/register", nil)
	registered := make(chan int, 1)
	go func() {
		registered <- other.post("/register", registerValues("concurrent@example.test", "Concurrent owner", "OwnerPassword123")).Code
	}()
	select {
	case status := <-registered:
		if status != 303 {
			t.Errorf("concurrent registration: %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Error("Telegram I/O blocked an account write")
	}
	// Unblock before cleanup closes the fake server, and join the mutation.
	release <- struct{}{}
	select {
	case status := <-connected:
		if status != 303 {
			t.Errorf("connection: %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not finish")
	}
}

func TestBotConnectionFailuresDoNotSaveOrExposeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, token, method, response, message string
		apiStatus, status                      int
	}{
		{"missing token", "", "", "", "توکن معتبر نیست", 200, 422},
		{"malformed token", "123:bad/../../secret", "", "", "توکن معتبر نیست", 200, 422},
		{"unauthorized", testBotToken, "getMe", `{"ok":false,"description":"` + testBotToken + `"}`, "توکن معتبر نیست", 401, 422},
		{"API rejected credentials", testBotToken, "getMe", `{"ok":false,"error_code":401}`, "توکن معتبر نیست", 200, 422},
		{"Telegram failure", testBotToken, "getMe", testBotToken, "ارتباط با تلگرام ممکن نشد", 500, 503},
		{"Telegram redirects", testBotToken, "getMe", testBotToken, "ارتباط با تلگرام ممکن نشد", 302, 503},
		{"rate limited by Telegram", testBotToken, "getMe", `{"ok":false,"error_code":429}`, "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"oversized response", testBotToken, "getMe", strings.Repeat("x", 65537), "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"malformed response", testBotToken, "getMe", "not json " + testBotToken, "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"not a Bot", testBotToken, "getMe", `{"ok":true,"result":{"id":123456,"is_bot":false,"first_name":"Mina","username":"mina_test_bot"}}`, "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"webhook inspection fails", testBotToken, "getWebhookInfo", "upstream failure " + testBotToken, "ارتباط با تلگرام ممکن نشد", 502, 503},
		{"missing webhook fields", testBotToken, "getWebhookInfo", `{"ok":true,"result":{}}`, "ارتباط با تلگرام ممکن نشد", 200, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.method == "" {
					t.Error("malformed token reached Telegram")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/"+tc.method) {
					if tc.apiStatus == 302 {
						w.Header().Set("Location", "/credential-forwarding-forbidden")
					}
					w.WriteHeader(tc.apiStatus)
					fmt.Fprint(w, tc.response)
				} else {
					fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Mina","username":"mina_test_bot"}}`)
				}
			})
			b.send(http.MethodGet, "/bots/connect", nil)
			got := b.post("/bots/connect", url.Values{"token": {tc.token}})
			if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.message) {
				t.Fatalf("connection failure: %d %s", got.Code, got.Body.String())
			}
			if tc.token != "" && strings.Contains(got.Body.String(), tc.token) {
				t.Fatal("credential reflected in error page")
			}
			list := b.send(http.MethodGet, "/bots", nil)
			if !strings.Contains(list.Body.String(), "هنوز رباتی نساخته") {
				t.Fatal("failed verification saved a Bot")
			}
		})
	}
}

func TestBotVerificationTransportFailureClearsToken(t *testing.T) {
	_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	})
	b.send(http.MethodGet, "/bots/connect", nil)
	got := b.post("/bots/connect", url.Values{"token": {testBotToken}})
	if got.Code != 503 || strings.Contains(got.Body.String(), testBotToken) || !strings.Contains(got.Body.String(), "ارتباط با تلگرام ممکن نشد") {
		t.Fatalf("transport failure: %d", got.Code)
	}
	if list := b.send(http.MethodGet, "/bots", nil); !strings.Contains(list.Body.String(), "هنوز رباتی نساخته") {
		t.Fatal("transport failure saved Bot")
	}
}

func TestBotInsertFailureDoesNotPersistOrExposeCredentials(t *testing.T) {
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Unsaved Bot","username":"unsaved_bot"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		}
	})
	// Inject a storage failure at the system boundary, including a credential
	// in its diagnostic to prove that arbitrary database errors are not exposed.
	if _, err := a.db.Exec("CREATE TRIGGER fail_bot_insert BEFORE INSERT ON bots BEGIN SELECT RAISE(ABORT, '" + testBotToken + "'); END"); err != nil {
		t.Fatal(err)
	}
	b.send(http.MethodGet, "/bots/connect", nil)
	got := b.post("/bots/connect", url.Values{"token": {testBotToken}})
	if got.Code != 500 || strings.Contains(got.Body.String(), testBotToken) || !strings.Contains(got.Body.String(), "ذخیرهٔ ربات ممکن نشد") {
		t.Fatalf("storage failure: %d", got.Code)
	}
	if list := b.send(http.MethodGet, "/bots", nil); strings.Contains(list.Body.String(), "Unsaved Bot") {
		t.Fatal("failed insert persisted a Bot")
	}
}

func TestBotConnectionShowsDeliveryConflictAndIsolatesOwners(t *testing.T) {
	const foreignURL = "https://example.test/secret-webhook-path?key=foreign-secret"
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Owner Bot","username":"owner_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			fmt.Fprintf(w, `{"ok":true,"result":{"url":%q,"pending_update_count":12}}`, foreignURL)
		default:
			t.Errorf("unexpected delivery operation: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	b.send(http.MethodGet, "/bots/connect", nil)
	got := b.post("/bots/connect", url.Values{"token": {testBotToken}})
	if got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	detail := b.send(http.MethodGet, "/bots/1", nil).Body.String() + b.send(http.MethodGet, "/bots/1/connection", nil).Body.String()
	for _, want := range []string{"از قبل تنظیم شده", "۱۲", "فعال نشده"} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing delivery observation %q", want)
		}
	}
	if strings.Contains(detail, foreignURL) || strings.Contains(detail, "foreign-secret") {
		t.Fatal("foreign service secret exposed")
	}
	if duplicate := b.post("/bots/connect", url.Values{"token": {testBotToken}}); duplicate.Code != 409 {
		t.Fatalf("same-owner duplicate: %d", duplicate.Code)
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send(http.MethodGet, "/register", nil)
	if registration := other.post("/register", registerValues("other-owner@example.test", "Other owner", "OwnerPassword123")); registration.Code != 303 {
		t.Fatalf("second owner: %d", registration.Code)
	}
	for _, path := range []string{"/bots/1", "/bots/999", "/bots/nope", "/bots/0"} {
		if got := other.send(http.MethodGet, path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "Owner Bot") {
			t.Errorf("cross-owner detail %s: %d", path, got.Code)
		}
	}
	for _, path := range []string{"/bots", "/dashboard"} {
		if got := other.send(http.MethodGet, path, nil); strings.Contains(got.Body.String(), "Owner Bot") || strings.Contains(got.Body.String(), "owner_bot") {
			t.Errorf("cross-owner list at %s", path)
		}
	}
	other.send(http.MethodGet, "/bots/connect", nil)
	if got := other.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 409 || strings.Contains(got.Body.String(), "Owner Bot") {
		t.Fatalf("cross-owner duplicate: %d", got.Code)
	}
	if retained := b.send(http.MethodGet, "/bots/1", nil); retained.Code != 200 || !strings.Contains(retained.Body.String(), "Owner Bot") {
		t.Fatal("duplicate changed original ownership")
	}
}

func TestBotRoutesRequireAuthenticationAndCSRF(t *testing.T) {
	a, owner, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unauthorized mutation reached Telegram")
		w.WriteHeader(500)
	})
	visitor := newAccountBrowser(t, a.server.Handler)
	for _, path := range []string{"/bots", "/bots/connect", "/bots/1"} {
		if got := visitor.send(http.MethodGet, path, nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Errorf("anonymous access at %s: %d", path, got.Code)
		}
	}
	visitor.send(http.MethodGet, "/register", nil)
	if got := visitor.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Errorf("anonymous mutation: %d", got.Code)
	}
	owner.send(http.MethodGet, "/bots/connect", nil)
	for _, csrf := range []string{"", "invalid"} {
		if got := owner.send(http.MethodPost, "/bots/connect", url.Values{"token": {testBotToken}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Errorf("CSRF %q: %d", csrf, got.Code)
		}
	}
	if got := owner.send(http.MethodGet, "/bots/connect?token="+testBotToken, nil); got.Code != 200 || strings.Contains(got.Body.String(), testBotToken) {
		t.Fatal("GET token exposed or consumed")
	}
	if got := owner.send(http.MethodPut, "/bots/connect", url.Values{"csrf_token": {owner.cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatalf("non-POST mutation: %d", got.Code)
	}
}

func TestBotConnectionSurvivesRestartWithEncryptedCredentials(t *testing.T) {
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Durable Bot","username":"durable_bot"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		}
	})
	b.send(http.MethodGet, "/bots/connect", nil)
	if got := b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	// Inspect credential storage solely for its encryption contract; business
	// persistence and ownership assertions continue through public HTTP below.
	var encrypted []byte
	var ownerID int64
	if err := a.db.QueryRow("SELECT encrypted_token, owner_id FROM bots").Scan(&encrypted, &ownerID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), testBotToken) {
		t.Fatal("token stored in plaintext")
	}
	block, _ := aes.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	aead, _ := cipher.NewGCM(block)
	if len(encrypted) < aead.NonceSize()+aead.Overhead() {
		t.Fatal("invalid credential ciphertext")
	}
	aad := []byte(fmt.Sprintf("piko:bot:v1:%d:123456", ownerID))
	clear, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], aad)
	if err != nil || string(clear) != testBotToken {
		t.Fatal("credential cannot be recovered with separate key")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], aad); err == nil {
		t.Fatal("credential tampering accepted")
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	for _, path := range []string{"/bots", "/bots/1", "/dashboard", "/account"} {
		got := b.send(http.MethodGet, path, nil)
		if got.Code != 200 || (path != "/account" && !strings.Contains(got.Body.String(), "Durable Bot")) {
			t.Errorf("restart at %s lost Bot/session: %d", path, got.Code)
		}
	}
	// Reapplying migrations is safe for the existing file and session.
	if err := database.Migrate(t.Context(), restarted.db, false); err != nil {
		t.Fatal(err)
	}
	if got := b.send(http.MethodGet, "/bots/1", nil); got.Code != 200 {
		t.Fatal("migration invalidated session")
	}
}

func botFixture(t *testing.T, api http.HandlerFunc) (*App, *accountBrowser, string) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := httptest.NewServer(api)
	t.Cleanup(fake.Close)
	cfg := Config{DatabasePath: path, SessionSecret: "bot-test-session-secret-with-32-characters", BotEncryptionKey: base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), LogLevel: "error"}
	a, err := newWithTelegram(t.Context(), cfg, telegram.NewClient(fake.URL, fake.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	b := newAccountBrowser(t, a.server.Handler)
	b.send(http.MethodGet, "/register", nil)
	if got := b.post("/register", registerValues("bot-owner@example.test", "مینا", "OwnerPassword123")); got.Code != http.StatusSeeOther {
		t.Fatalf("register: %d", got.Code)
	}
	return a, b, path
}

func TestBotConnectionVerifiesAndPersistsWithoutActivating(t *testing.T) {
	var methods []string
	_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot"+testBotToken+"/")
		methods = append(methods, method)
		switch method {
		case "getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Mina <bot>","username":"mina_test_bot"}}`)
		case "getWebhookInfo":
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":7}}`)
		default:
			t.Errorf("verification changed delivery: %s", method)
			w.WriteHeader(500)
		}
	})
	form := b.send(http.MethodGet, "/bots/connect", nil)
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), "BotFather") || !strings.Contains(form.Body.String(), `type="password"`) {
		t.Fatalf("connection form: %d", form.Code)
	}
	got := b.post("/bots/connect", url.Values{"token": {testBotToken}})
	if got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/bots/1" {
		t.Fatalf("connect: %d %s", got.Code, got.Body.String())
	}
	for _, path := range []string{"/bots", "/bots/1", "/dashboard"} {
		page := b.send(http.MethodGet, path, nil)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Mina &lt;bot&gt;") || strings.Contains(page.Body.String(), testBotToken) {
			t.Fatalf("saved Bot at %s: %d %s", path, page.Code, page.Body.String())
		}
	}
	detail := b.send(http.MethodGet, "/bots/1", nil).Body.String() + b.send(http.MethodGet, "/bots/1/connection", nil).Body.String()
	for _, want := range []string{"mina_test_bot", "تأیید شده", "فعال نشده", "۷", "سرویس دیگری"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if strings.Join(methods, ",") != "getMe,getWebhookInfo" {
		t.Fatalf("Telegram operations: %v", methods)
	}
}
