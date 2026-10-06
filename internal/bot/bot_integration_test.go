package bot_test

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/testsupport"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBotMigrationPreservesExistingAccountAndSession(t *testing.T) {
	db, path := testsupport.MigratedSQLite(t, t.Context())
	// Roll back later migrations in this disposable file to represent
	// an installed pre-Bot schema; registration still uses its existing HTTP seam.
	fixture.RollbackToMigration(t, db, "000003_persian_only")
	router, _ := fixture.AccountTestRouter(t, db)
	b := fixture.NewAccountBrowser(t, router)
	b.Send(http.MethodGet, "/register", nil)
	if got := b.Post("/register", fixture.RegisterValues("existing@example.test", "نام پیشین", "UnchangedPassword123")); got.Code != 303 {
		t.Fatalf("old schema registration: %d", got.Code)
	}
	if err := database.Migrate(t.Context(), db, false); err != nil {
		t.Fatal(err)
	}
	a, err := fixture.New(t.Context(), httpapp.Config{DatabasePath: path, SessionSecret: "unchanged-session-secret-at-least-32", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", LogLevel: "error"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.DB.Close() })
	b.Router = a.Handler
	account := b.Send(http.MethodGet, "/account", nil)
	if account.Code != 200 || !strings.Contains(account.Body.String(), "نام پیشین") || !strings.Contains(account.Body.String(), "existing@example.test") {
		t.Fatal("forward migration lost account or session")
	}
	b.Post("/logout", url.Values{})
	b.Send(http.MethodGet, "/login", nil)
	if got := b.Post("/login", url.Values{"email": {"existing@example.test"}, "password": {"UnchangedPassword123"}}); got.Code != 303 {
		t.Fatalf("migration changed password: %d", got.Code)
	}
	if got := b.Send(http.MethodGet, "/bots", nil); got.Code != 200 {
		t.Fatal("migrated owner cannot access Bots")
	}
}

func TestSlowTelegramVerificationDoesNotLockAccountWrites(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			close(started)
			<-release
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Slow Bot","username":"slow_bot"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		}
	})
	t.Cleanup(func() { close(release) })
	b.Send(http.MethodGet, "/bots/connect", nil)
	connected := make(chan int, 1)
	go func() { connected <- b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}).Code }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("verification never started")
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send(http.MethodGet, "/register", nil)
	registered := make(chan int, 1)
	go func() {
		registered <- other.Post("/register", fixture.RegisterValues("concurrent@example.test", "Concurrent owner", "OwnerPassword123")).Code
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
		APIStatus, status                      int
	}{
		{"missing token", "", "", "", "توکن معتبر نیست", 200, 422},
		{"malformed token", "123:bad/../../secret", "", "", "توکن معتبر نیست", 200, 422},
		{"unauthorized", fixture.TestBotToken, "getMe", `{"ok":false,"description":"` + fixture.TestBotToken + `"}`, "توکن معتبر نیست", 401, 422},
		{"API rejected credentials", fixture.TestBotToken, "getMe", `{"ok":false,"error_code":401}`, "توکن معتبر نیست", 200, 422},
		{"Telegram failure", fixture.TestBotToken, "getMe", fixture.TestBotToken, "ارتباط با تلگرام ممکن نشد", 500, 503},
		{"Telegram redirects", fixture.TestBotToken, "getMe", fixture.TestBotToken, "ارتباط با تلگرام ممکن نشد", 302, 503},
		{"rate limited by Telegram", fixture.TestBotToken, "getMe", `{"ok":false,"error_code":429}`, "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"oversized response", fixture.TestBotToken, "getMe", strings.Repeat("x", 65537), "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"malformed response", fixture.TestBotToken, "getMe", "not json " + fixture.TestBotToken, "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"not a Bot", fixture.TestBotToken, "getMe", `{"ok":true,"result":{"id":123456,"is_bot":false,"first_name":"Mina","username":"mina_test_bot"}}`, "ارتباط با تلگرام ممکن نشد", 200, 503},
		{"webhook inspection fails", fixture.TestBotToken, "getWebhookInfo", "upstream failure " + fixture.TestBotToken, "ارتباط با تلگرام ممکن نشد", 502, 503},
		{"missing webhook fields", fixture.TestBotToken, "getWebhookInfo", `{"ok":true,"result":{}}`, "ارتباط با تلگرام ممکن نشد", 200, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.method == "" {
					t.Error("malformed token reached Telegram")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/"+tc.method) {
					if tc.APIStatus == 302 {
						w.Header().Set("Location", "/credential-forwarding-forbidden")
					}
					w.WriteHeader(tc.APIStatus)
					fmt.Fprint(w, tc.response)
				} else {
					fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Mina","username":"mina_test_bot"}}`)
				}
			})
			b.Send(http.MethodGet, "/bots/connect", nil)
			got := b.Post("/bots/connect", url.Values{"token": {tc.token}})
			if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.message) {
				t.Fatalf("connection failure: %d %s", got.Code, got.Body.String())
			}
			if tc.token != "" && strings.Contains(got.Body.String(), tc.token) {
				t.Fatal("credential reflected in error page")
			}
			list := b.Send(http.MethodGet, "/bots", nil)
			if !strings.Contains(list.Body.String(), "هنوز رباتی نساخته") {
				t.Fatal("failed verification saved a Bot")
			}
		})
	}
}

func TestBotVerificationTransportFailureClearsToken(t *testing.T) {
	_, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	})
	b.Send(http.MethodGet, "/bots/connect", nil)
	got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}})
	if got.Code != 503 || strings.Contains(got.Body.String(), fixture.TestBotToken) || !strings.Contains(got.Body.String(), "ارتباط با تلگرام ممکن نشد") {
		t.Fatalf("transport failure: %d", got.Code)
	}
	if list := b.Send(http.MethodGet, "/bots", nil); !strings.Contains(list.Body.String(), "هنوز رباتی نساخته") {
		t.Fatal("transport failure saved Bot")
	}
}

func TestBotInsertFailureDoesNotPersistOrExposeCredentials(t *testing.T) {
	a, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Unsaved Bot","username":"unsaved_bot"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		}
	})
	// Inject a storage failure at the system boundary, including a credential
	// in its diagnostic to prove that arbitrary database errors are not exposed.
	if _, err := a.DB.Exec("CREATE TRIGGER fail_bot_insert BEFORE INSERT ON bots BEGIN SELECT RAISE(ABORT, '" + fixture.TestBotToken + "'); END"); err != nil {
		t.Fatal(err)
	}
	b.Send(http.MethodGet, "/bots/connect", nil)
	got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}})
	if got.Code != 500 || strings.Contains(got.Body.String(), fixture.TestBotToken) || !strings.Contains(got.Body.String(), "ذخیرهٔ ربات ممکن نشد") {
		t.Fatalf("storage failure: %d", got.Code)
	}
	if list := b.Send(http.MethodGet, "/bots", nil); strings.Contains(list.Body.String(), "Unsaved Bot") {
		t.Fatal("failed insert persisted a Bot")
	}
}

func TestBotConnectionShowsDeliveryConflictAndIsolatesOwners(t *testing.T) {
	const foreignURL = "https://example.test/secret-webhook-path?key=foreign-secret"
	a, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
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
	b.Send(http.MethodGet, "/bots/connect", nil)
	got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}})
	if got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	detail := b.Send(http.MethodGet, "/bots/1", nil).Body.String() + b.Send(http.MethodGet, "/bots/1/connection", nil).Body.String()
	for _, want := range []string{"از قبل تنظیم شده", "۱۲", "فعال نشده"} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing delivery observation %q", want)
		}
	}
	if strings.Contains(detail, foreignURL) || strings.Contains(detail, "foreign-secret") {
		t.Fatal("foreign service secret exposed")
	}
	if duplicate := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); duplicate.Code != 409 {
		t.Fatalf("same-owner duplicate: %d", duplicate.Code)
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send(http.MethodGet, "/register", nil)
	if registration := other.Post("/register", fixture.RegisterValues("other-owner@example.test", "Other owner", "OwnerPassword123")); registration.Code != 303 {
		t.Fatalf("second owner: %d", registration.Code)
	}
	for _, path := range []string{"/bots/1", "/bots/999", "/bots/nope", "/bots/0"} {
		if got := other.Send(http.MethodGet, path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "Owner Bot") {
			t.Errorf("cross-owner detail %s: %d", path, got.Code)
		}
	}
	for _, path := range []string{"/bots", "/dashboard"} {
		if got := other.Send(http.MethodGet, path, nil); strings.Contains(got.Body.String(), "Owner Bot") || strings.Contains(got.Body.String(), "owner_bot") {
			t.Errorf("cross-owner list at %s", path)
		}
	}
	other.Send(http.MethodGet, "/bots/connect", nil)
	if got := other.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 409 || strings.Contains(got.Body.String(), "Owner Bot") {
		t.Fatalf("cross-owner duplicate: %d", got.Code)
	}
	if retained := b.Send(http.MethodGet, "/bots/1", nil); retained.Code != 200 || !strings.Contains(retained.Body.String(), "Owner Bot") {
		t.Fatal("duplicate changed original ownership")
	}
}

func TestBotRoutesRequireAuthenticationAndCSRF(t *testing.T) {
	a, owner, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unauthorized mutation reached Telegram")
		w.WriteHeader(500)
	})
	visitor := fixture.NewAccountBrowser(t, a.Handler)
	for _, path := range []string{"/bots", "/bots/connect", "/bots/1"} {
		if got := visitor.Send(http.MethodGet, path, nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Errorf("anonymous access at %s: %d", path, got.Code)
		}
	}
	visitor.Send(http.MethodGet, "/register", nil)
	if got := visitor.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Errorf("anonymous mutation: %d", got.Code)
	}
	owner.Send(http.MethodGet, "/bots/connect", nil)
	for _, csrf := range []string{"", "invalid"} {
		if got := owner.Send(http.MethodPost, "/bots/connect", url.Values{"token": {fixture.TestBotToken}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Errorf("CSRF %q: %d", csrf, got.Code)
		}
	}
	if got := owner.Send(http.MethodGet, "/bots/connect?token="+fixture.TestBotToken, nil); got.Code != 200 || strings.Contains(got.Body.String(), fixture.TestBotToken) {
		t.Fatal("GET token exposed or consumed")
	}
	if got := owner.Send(http.MethodPut, "/bots/connect", url.Values{"csrf_token": {owner.Cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatalf("non-POST mutation: %d", got.Code)
	}
}

func TestBotConnectionSurvivesRestartWithEncryptedCredentials(t *testing.T) {
	a, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Durable Bot","username":"durable_bot"}}`)
		} else {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		}
	})
	b.Send(http.MethodGet, "/bots/connect", nil)
	if got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatalf("connect: %d", got.Code)
	}
	// Inspect credential storage solely for its encryption contract; business
	// persistence and ownership assertions continue through public HTTP below.
	var encrypted []byte
	var ownerID int64
	if err := a.DB.QueryRow("SELECT encrypted_token, owner_id FROM bots").Scan(&encrypted, &ownerID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), fixture.TestBotToken) {
		t.Fatal("token stored in plaintext")
	}
	block, _ := aes.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	aead, _ := cipher.NewGCM(block)
	if len(encrypted) < aead.NonceSize()+aead.Overhead() {
		t.Fatal("invalid credential ciphertext")
	}
	aad := []byte(fmt.Sprintf("piko:bot:v1:%d:123456", ownerID))
	clear, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], aad)
	if err != nil || string(clear) != fixture.TestBotToken {
		t.Fatal("credential cannot be recovered with separate key")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], aad); err == nil {
		t.Fatal("credential tampering accepted")
	}
	if err := a.DB.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	for _, path := range []string{"/bots", "/bots/1", "/dashboard", "/account"} {
		got := b.Send(http.MethodGet, path, nil)
		if got.Code != 200 || (path != "/account" && !strings.Contains(got.Body.String(), "Durable Bot")) {
			t.Errorf("restart at %s lost Bot/session: %d", path, got.Code)
		}
	}
	// Reapplying migrations is safe for the existing file and session.
	if err := database.Migrate(t.Context(), restarted.DB, false); err != nil {
		t.Fatal(err)
	}
	if got := b.Send(http.MethodGet, "/bots/1", nil); got.Code != 200 {
		t.Fatal("migration invalidated session")
	}
}

func TestBotConnectionVerifiesAndPersistsWithoutActivating(t *testing.T) {
	var methods []string
	_, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/bot"+fixture.TestBotToken+"/")
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
	form := b.Send(http.MethodGet, "/bots/connect", nil)
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), "BotFather") || !strings.Contains(form.Body.String(), `type="password"`) {
		t.Fatalf("connection form: %d", form.Code)
	}
	got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}})
	if got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/bots/1" {
		t.Fatalf("connect: %d %s", got.Code, got.Body.String())
	}
	for _, path := range []string{"/bots", "/bots/1", "/dashboard"} {
		page := b.Send(http.MethodGet, path, nil)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Mina &lt;bot&gt;") || strings.Contains(page.Body.String(), fixture.TestBotToken) {
			t.Fatalf("saved Bot at %s: %d %s", path, page.Code, page.Body.String())
		}
	}
	detail := b.Send(http.MethodGet, "/bots/1", nil).Body.String() + b.Send(http.MethodGet, "/bots/1/connection", nil).Body.String()
	for _, want := range []string{"mina_test_bot", "تأیید شده", "فعال نشده", "۷", "سرویس دیگری"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if strings.Join(methods, ",") != "getMe,getWebhookInfo" {
		t.Fatalf("Telegram operations: %v", methods)
	}
}
