package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"buildx/internal/locale"
	"buildx/internal/platform/database/dbgen"
	"buildx/internal/testsupport"
	"github.com/jackc/pgx/v5"
)

type capturedMailQueue struct{ challenges []string }

func (q *capturedMailQueue) EnqueueChallenge(_ context.Context, _ pgx.Tx, nonce string) error {
	q.challenges = append(q.challenges, nonce)
	return nil
}

func TestVerificationStorageFailureKeepsFormAndOmitsPassword(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, _ := testsupport.MigratedPostgres(t, ctx)
	queue := &capturedMailQueue{}
	accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("test-secret-long-enough-for-account-links"), "http://localhost:8080", accountCatalog(t))
	registration, err := accounts.Register(ctx, "verification-feedback@example.test", "Mina", "private-password-123", "en")
	if err != nil {
		t.Fatal(err)
	}
	message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.Fields(message.Body)[0])
	if err != nil {
		t.Fatal(err)
	}
	// Fail at the database boundary after challenge loading, without replacing
	// service logic or exposing a test-only production interface.
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_verification() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test storage failure'; END $$;
		CREATE TRIGGER reject_verification BEFORE UPDATE OF email_verified_at ON users FOR EACH ROW EXECUTE FUNCTION reject_verification()`); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(nil, accounts, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	for _, language := range []string{"en", "fa"} {
		for _, requirePassword := range []bool{true, false} {
			form := url.Values{"token": {link.Query().Get("token")}, "password": {"private-password-123"}}
			request := withLanguageLocale(t, httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(form.Encode())), language)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if !requirePassword {
				request.AddCookie(&http.Cookie{Name: SignupCookie, Value: registration.Receipt})
			}
			response := httptest.NewRecorder()
			handler.Verify(response, request)
			body := response.Body.String()
			if response.Code != 500 || !strings.Contains(body, `action="/verify"`) || strings.Contains(body, `id="verify-password"`) != requirePassword || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, locale.T(request.Context(), "auth.verify.error.unavailable")) {
				t.Errorf("%s storage failure lost verification context", language)
			}
			if strings.Contains(body, "private-password-123") || strings.Contains(body, "test storage failure") || strings.Contains(body, `aria-invalid="true"`) {
				t.Errorf("%s storage failure exposed secret/details or assigned a field", language)
			}
		}
	}
}

func TestRecoveryAndAccountPagesUseSelectedLanguage(t *testing.T) {
	for _, tc := range []struct {
		language, direction, forgot, account, invalid, menu, appearance string
	}{
		{"fa", "rtl", "بازیابی حساب", "جزئیات حساب", "فرم نامعتبر است.", "خروج", "حالت نمایش"},
		{"en", "ltr", "Recover your account", "Account details", "Invalid form.", "Log out", "Theme preference"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			handler := NewHandler(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			check := func(path, method, body string, action func(http.ResponseWriter, *http.Request), status int, want string) *httptest.ResponseRecorder {
				t.Helper()
				request := withLanguageLocale(t, httptest.NewRequest(method, path, strings.NewReader(body)), tc.language)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if path == "/account" {
					request = request.WithContext(WithUser(request.Context(), User{DisplayName: "نام Rivera", Email: "owner@example.test"}))
				}
				response := httptest.NewRecorder()
				action(response, request)
				for _, expected := range []string{`lang="` + tc.language + `"`, `dir="` + tc.direction + `"`, want} {
					if response.Code != status || !strings.Contains(response.Body.String(), expected) {
						t.Errorf("%s status=%d missing %q", path, response.Code, expected)
					}
				}
				return response
			}
			check("/password/forgot", http.MethodGet, "", handler.ForgotForm, http.StatusOK, tc.forgot)
			check("/password/forgot", http.MethodPost, "%", handler.Forgot, http.StatusUnprocessableEntity, tc.invalid)
			check("/password/reset", http.MethodPost, "%", handler.Reset, http.StatusUnprocessableEntity, tc.invalid)
			account := check("/account", http.MethodGet, "", handler.Profile, http.StatusOK, tc.account)
			for _, want := range []string{tc.menu, tc.appearance, `<bdi dir="auto">نام Rivera</bdi>`, `<bdi dir="ltr">owner@example.test</bdi>`} {
				if !strings.Contains(account.Body.String(), want) {
					t.Errorf("account shell missing %q", want)
				}
			}
		})
	}
}

func TestAccountMalformedFormsUseSelectedLanguage(t *testing.T) {
	for _, tc := range []struct{ language, direction, feedback string }{
		{"fa", "rtl", "فرم نامعتبر است."},
		{"en", "ltr", "Invalid form."},
	} {
		t.Run(tc.language, func(t *testing.T) {
			handler := NewHandler(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			for _, action := range []struct {
				path   string
				handle func(http.ResponseWriter, *http.Request)
			}{
				{"/register", handler.Register},
				{"/verify/resend", handler.ResendVerification},
				{"/verify", handler.Verify},
				{"/verify/cancel", handler.Cancel},
			} {
				request := withLanguageLocale(t, httptest.NewRequest(http.MethodPost, action.path, strings.NewReader("%")), tc.language)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				action.handle(response, request)
				if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `lang="`+tc.language+`"`) || !strings.Contains(response.Body.String(), `dir="`+tc.direction+`"`) || !strings.Contains(response.Body.String(), tc.feedback) {
					t.Errorf("%s malformed form status=%d feedback or direction missing", action.path, response.Code)
				}
			}
		})
	}
}

// Queued jobs hold only a nonce, so the delivery lookup must use the current account language.
func TestQueuedVerificationUsesLanguageAtDelivery(t *testing.T) {
	for _, tc := range []struct {
		registered, delivered, subject, phrase string
	}{
		{"fa", "en", "Email verification", "Verify your email to activate your account."},
		{"en", "fa", "تأیید ایمیل", "برای فعال\u200cسازی حساب، ایمیل خود را تأیید کنید."},
	} {
		t.Run(tc.registered+"_to_"+tc.delivered, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			pool, _ := testsupport.MigratedPostgres(t, ctx)
			queue := &capturedMailQueue{}
			accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
			email := fmt.Sprintf("language-%d@example.test", time.Now().UnixNano())
			registration, err := accounts.Register(ctx, email, "نام Rivera", "a-strong-password-1", tc.registered)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", registration.Account.ID)
			})
			if len(queue.challenges) != 1 {
				t.Fatalf("queued challenges=%d", len(queue.challenges))
			}
			if err := NewService(dbgen.New(pool)).SetLanguage(ctx, registration.Account.ID, tc.delivered); err != nil {
				t.Fatal(err)
			}
			message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			if message.To != email || message.Subject != tc.subject || !strings.Contains(message.Body, tc.phrase) {
				t.Fatalf("delivery language=%s message=%+v", tc.delivered, message)
			}
			if !strings.Contains(message.Body, "/verify/cancel?token=") {
				t.Fatal("localized message omitted cancellation link")
			}
		})
	}
}

func TestQueuedRecoveryUsesLanguageAtDelivery(t *testing.T) {
	for _, tc := range []struct {
		registered, delivered, subject, phrase string
	}{
		{"fa", "en", "Reset your password", "Use this link to set a new password."},
		{"en", "fa", "بازیابی گذرواژه", "برای تعیین گذرواژهٔ تازه از این پیوند استفاده کنید."},
	} {
		t.Run(tc.registered+"_to_"+tc.delivered, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			pool, _ := testsupport.MigratedPostgres(t, ctx)
			queue := &capturedMailQueue{}
			credentials := NewService(dbgen.New(pool))
			accounts := NewAccountService(NewAccountRepository(pool, queue), credentials, []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
			email := fmt.Sprintf("recovery-language-%d@example.test", time.Now().UnixNano())
			registration, err := accounts.Register(ctx, email, "نام Rivera", "a-strong-password-1", tc.registered)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", registration.Account.ID)
			})
			verifyMail, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			verifyLink, _ := url.Parse(strings.Fields(verifyMail.Body)[0])
			if _, err := accounts.CompleteVerification(ctx, verifyLink.Query().Get("token"), VerificationEvidence{SignupReceipt: registration.Receipt}); err != nil {
				t.Fatal(err)
			}
			if err := accounts.RequestReset(ctx, email); err != nil {
				t.Fatal(err)
			}
			if len(queue.challenges) != 2 {
				t.Fatalf("queued challenges=%d", len(queue.challenges))
			}
			if err := credentials.SetLanguage(ctx, registration.Account.ID, tc.delivered); err != nil {
				t.Fatal(err)
			}
			message, err := accounts.MessageForChallenge(ctx, queue.challenges[1])
			if err != nil {
				t.Fatal(err)
			}
			if message.To != email || message.Subject != tc.subject || !strings.Contains(message.Body, tc.phrase) || !strings.Contains(message.Body, "/password/reset?token=") {
				t.Fatalf("delivery language=%s message=%+v", tc.delivered, message)
			}
		})
	}
}

func TestRecoveryRouteJourneyInBothLanguages(t *testing.T) {
	for _, tc := range []struct {
		language, direction, sent, reset, invalid, expired string
	}{
		{"fa", "rtl", "اگر این نشانی به حساب تأییدشده\u200cای تعلق داشته باشد", "گذرواژهٔ تازه تعیین کنید", "گذرواژه\u200cای با دست\u200cکم ۱۲ نویسه وارد کنید.", "پیوند در دسترس نیست"},
		{"en", "ltr", "If this is a verified account", "Set a new password", "Use a password of at least 12 characters.", "Link unavailable"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			pool, _ := testsupport.MigratedPostgres(t, ctx)
			queue := &capturedMailQueue{}
			accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
			email := fmt.Sprintf("recovery-route-%d@example.test", time.Now().UnixNano())
			registration, err := accounts.Register(ctx, email, "Recovery", "original-password-1", tc.language)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", registration.Account.ID)
			})
			verifyMail, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			verifyLink, _ := url.Parse(strings.Fields(verifyMail.Body)[0])
			if _, err := accounts.CompleteVerification(ctx, verifyLink.Query().Get("token"), VerificationEvidence{SignupReceipt: registration.Receipt}); err != nil {
				t.Fatal(err)
			}
			handler := NewHandler(NewService(dbgen.New(pool)), accounts, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			call := func(method, path string, form url.Values, action func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
				t.Helper()
				if form == nil {
					form = url.Values{}
				}
				request := withLanguageLocale(t, httptest.NewRequest(method, path, strings.NewReader(form.Encode())), tc.language)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				action(response, request)
				return response
			}
			assertPage := func(response *httptest.ResponseRecorder, status int, want string) {
				t.Helper()
				if response.Code != status || !strings.Contains(response.Body.String(), `lang="`+tc.language+`"`) || !strings.Contains(response.Body.String(), `dir="`+tc.direction+`"`) || !strings.Contains(response.Body.String(), want) {
					t.Fatalf("status=%d page missing %q", response.Code, want)
				}
			}
			known := call(http.MethodPost, "/password/forgot", url.Values{"email": {email}}, handler.Forgot)
			unknown := call(http.MethodPost, "/password/forgot", url.Values{"email": {"unknown@example.test"}}, handler.Forgot)
			assertPage(known, http.StatusOK, tc.sent)
			if known.Body.String() != unknown.Body.String() || strings.Contains(known.Body.String(), email) {
				t.Fatal("recovery response disclosed account status")
			}
			if len(queue.challenges) != 2 {
				t.Fatalf("queued challenges=%d", len(queue.challenges))
			}
			resetMail, err := accounts.MessageForChallenge(ctx, queue.challenges[1])
			if err != nil {
				t.Fatal(err)
			}
			resetLink, _ := url.Parse(strings.Fields(resetMail.Body)[0])
			token := resetLink.Query().Get("token")
			assertPage(call(http.MethodGet, "/password/reset?token="+url.QueryEscape(token), nil, handler.ResetForm), http.StatusOK, tc.reset)
			assertPage(call(http.MethodPost, "/password/reset", url.Values{"token": {token}, "password": {"short"}}, handler.Reset), http.StatusUnprocessableEntity, tc.invalid)
			complete := call(http.MethodPost, "/password/reset", url.Values{"token": {token}, "password": {"replacement-password"}}, handler.Reset)
			if complete.Code != http.StatusSeeOther || complete.Header().Get("Location") != "/login" {
				t.Fatalf("reset status=%d location=%q", complete.Code, complete.Header().Get("Location"))
			}
			assertPage(call(http.MethodGet, "/password/reset?token="+url.QueryEscape(token), nil, handler.ResetForm), http.StatusUnprocessableEntity, tc.expired)
			assertPage(call(http.MethodPost, "/password/reset", url.Values{"token": {token}, "password": {"another-password"}}, handler.Reset), http.StatusUnprocessableEntity, tc.expired)
		})
	}
}

func TestPendingAccountRequiresEmailVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, _ := testsupport.MigratedPostgres(t, ctx)
	queue := &capturedMailQueue{}
	accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
	email := fmt.Sprintf("pending-%d@example.test", time.Now().UnixNano())
	registration, err := accounts.Register(ctx, email, "Pending", "a-strong-password-1", "fa")
	if err != nil {
		t.Fatal(err)
	}
	account := registration.Account
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", account.ID) })
	credentials := NewService(dbgen.New(pool))
	pending, err := credentials.Authenticate(ctx, email, "a-strong-password-1")
	if err != nil || pending.Verified {
		t.Fatalf("pending authentication: account=%+v err=%v", pending, err)
	}
	if len(queue.challenges) != 1 {
		t.Fatalf("queued emails=%d, want 1", len(queue.challenges))
	}
	if err := accounts.RequestReset(ctx, email); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ResendVerification(ctx, email); err != nil {
		t.Fatal(err)
	}
	if len(queue.challenges) != 1 {
		t.Fatalf("pending recovery or immediate resend queued mail: %d", len(queue.challenges))
	}
	message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	link := strings.Fields(message.Body)[0]
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CompleteVerification(ctx, parsed.Query().Get("token"), VerificationEvidence{SignupReceipt: registration.Receipt}); err != nil {
		t.Fatal(err)
	}
	verified, err := credentials.Authenticate(ctx, email, "a-strong-password-1")
	if err != nil || !verified.Verified {
		t.Fatalf("verified authentication: account=%+v err=%v", verified, err)
	}
	if _, err := accounts.CompleteVerification(ctx, parsed.Query().Get("token"), VerificationEvidence{SignupReceipt: registration.Receipt}); err == nil {
		t.Fatal("verification link was reusable")
	}
}

func TestRecoveryRevokesSessionsAndConsumesLink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, _ := testsupport.MigratedPostgres(t, ctx)
	queue := &capturedMailQueue{}
	accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
	email := fmt.Sprintf("recovery-%d@example.test", time.Now().UnixNano())
	registration, err := accounts.Register(ctx, email, "Recovery", "original-password-1", "fa")
	if err != nil {
		t.Fatal(err)
	}
	account := registration.Account
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", account.ID) })
	verifyMessage, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	verifyURL, _ := url.Parse(strings.Fields(verifyMessage.Body)[0])
	if _, err := accounts.CompleteVerification(ctx, verifyURL.Query().Get("token"), VerificationEvidence{SignupReceipt: registration.Receipt}); err != nil {
		t.Fatal(err)
	}
	sessions := NewService(dbgen.New(pool))
	cookie, _, _, err := sessions.NewSession(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.RequestReset(ctx, email); err != nil {
		t.Fatal(err)
	}
	resetMessage, err := accounts.MessageForChallenge(ctx, queue.challenges[1])
	if err != nil {
		t.Fatal(err)
	}
	resetURL, _ := url.Parse(strings.Fields(resetMessage.Body)[0])
	token := resetURL.Query().Get("token")
	if err := accounts.Reset(ctx, token, "replacement-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Authenticate(ctx, email, "original-password-1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password still works: %v", err)
	}
	if user, err := sessions.Authenticate(ctx, email, "replacement-password"); err != nil || !user.Verified {
		t.Fatalf("new password or verified state lost: user=%+v err=%v", user, err)
	}
	if _, err := sessions.LoadSession(ctx, cookie); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("old session still works: %v", err)
	}
	if err := accounts.Reset(ctx, token, "another-password"); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("reset link reusable: %v", err)
	}
	if err := accounts.RequestReset(ctx, email); err != nil {
		t.Fatal(err)
	}
	if len(queue.challenges) != 2 {
		t.Fatal("password reset erased the persistent email cooldown")
	}
}

func TestMailboxOwnerCanCancelPendingAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, _ := testsupport.MigratedPostgres(t, ctx)
	queue := &capturedMailQueue{}
	accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
	email := fmt.Sprintf("cancel-%d@example.test", time.Now().UnixNano())
	registration, err := accounts.Register(ctx, email, "Unwanted", "attacker-password-1", "fa")
	if err != nil {
		t.Fatal(err)
	}
	account := registration.Account
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email) })
	message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	cancelURL, _ := url.Parse(strings.Fields(message.Body)[len(strings.Fields(message.Body))-1])
	token := cancelURL.Query().Get("token")
	if err := accounts.CancelPending(ctx, token); err != nil {
		t.Fatal(err)
	}
	credentials := NewService(dbgen.New(pool))
	if _, err := credentials.Authenticate(ctx, email, "attacker-password-1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cancelled account still authenticates: %v", err)
	}
	if _, err := accounts.Register(ctx, email, "Owner", "owner-password-123", "fa"); err != nil {
		t.Fatalf("email remains occupied: %v", err)
	}
	if err := accounts.CancelPending(ctx, token); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("cancellation link reusable: %v", err)
	}
	_ = account
}

func TestRegistrationResponseDoesNotRevealExistingAddress(t *testing.T) {
	for _, tc := range []struct{ language, direction, heading string }{
		{"fa", "rtl", "ایمیل خود را بررسی کنید"},
		{"en", "ltr", "Check your email"},
	} {
		t.Run(tc.language, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			pool, _ := testsupport.MigratedPostgres(t, ctx)
			email := fmt.Sprintf("uniform-%d@example.test", time.Now().UnixNano())
			t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email) })
			queue := &capturedMailQueue{}
			accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
			handler := NewHandler(NewService(dbgen.New(pool)), accounts, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			passwordError, emailError := "Use at least 6 characters, including a letter and a number.", "Enter a valid email address."
			if tc.language == "fa" {
				passwordError, emailError = "دست\u200cکم ۶ نویسه شامل یک حرف و یک عدد وارد کنید.", "یک نشانی ایمیل معتبر وارد کنید."
			}
			for _, invalid := range []struct{ email, password, feedback string }{
				{email, "short", passwordError},
				{"invalid-email", "a-valid-password-123", emailError},
			} {
				form := url.Values{"email": {invalid.email}, "display_name": {"Applicant"}, "password": {invalid.password}}
				request := withLanguageLocale(t, httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode())), tc.language)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				handler.Register(response, request)
				if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), invalid.feedback) || !strings.Contains(response.Body.String(), `dir="`+tc.direction+`"`) {
					t.Fatalf("validation feedback status=%d missing %q", response.Code, invalid.feedback)
				}
			}
			var bodies []string
			var receipts []string
			for i := 0; i < 2; i++ {
				form := url.Values{"email": {email}, "display_name": {"Applicant"}, "password": {"a-valid-password-123"}}
				request := withLanguageLocale(t, httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode())), tc.language)
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				handler.Register(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("attempt %d status=%d", i, response.Code)
				}
				for _, want := range []string{`lang="` + tc.language + `"`, `dir="` + tc.direction + `"`, tc.heading} {
					if !strings.Contains(response.Body.String(), want) {
						t.Errorf("attempt %d missing %q", i, want)
					}
				}
				bodies = append(bodies, response.Body.String())
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != SignupCookie || !cookies[0].HttpOnly || cookies[0].Value == "" {
					t.Fatalf("attempt %d cookies do not have uniform limited receipt", i)
				}
				receipts = append(receipts, cookies[0].Value)
			}
			if _, err := accounts.RecentSignup(ctx, receipts[0]); err != nil {
				t.Fatalf("fresh signup receipt cannot complete verification: %v", err)
			}
			if _, err := accounts.RecentSignup(ctx, receipts[1]); !errors.Is(err, ErrInvalidChallenge) {
				t.Fatalf("duplicate receipt unexpectedly grants account access: %v", err)
			}
			if bodies[0] != bodies[1] {
				t.Fatal("registration page revealed whether the account existed")
			}
			if len(queue.challenges) != 1 {
				t.Fatalf("duplicate registration queued mail: %d", len(queue.challenges))
			}
		})
	}
}

func TestVerificationJourneyRendersBothLanguages(t *testing.T) {
	for _, tc := range []struct {
		language, direction, pending, verify, cancel, cancelled, expired, passwordError string
	}{
		{"fa", "rtl", "ایمیل خود را تأیید کنید", "ایمیل خود را تأیید کنید", "این حساب در انتظار تأیید را لغو کنید", "حساب در انتظار تأیید لغو شد", "پیوند در دسترس نیست", "گذرواژه نادرست است."},
		{"en", "ltr", "Verify your email", "Confirm your email", "Cancel this pending account", "Pending account cancelled", "Link unavailable", "Invalid password."},
	} {
		t.Run(tc.language, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			pool, _ := testsupport.MigratedPostgres(t, ctx)
			queue := &capturedMailQueue{}
			accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
			email := fmt.Sprintf("journey-%d@example.test", time.Now().UnixNano())
			registration, err := accounts.Register(ctx, email, "نام Rivera", "correct-password-123", tc.language)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", registration.Account.ID)
			})
			message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			link, err := url.Parse(strings.Fields(message.Body)[0])
			if err != nil {
				t.Fatal(err)
			}
			token := link.Query().Get("token")
			handler := NewHandler(NewService(dbgen.New(pool)), accounts, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
			call := func(method, path string, form url.Values, action func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
				t.Helper()
				var body *strings.Reader
				if form == nil {
					body = strings.NewReader("")
				} else {
					body = strings.NewReader(form.Encode())
				}
				request := withLanguageLocale(t, httptest.NewRequest(method, path, body), tc.language)
				if form != nil {
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				response := httptest.NewRecorder()
				action(response, request)
				return response
			}
			assertPage := func(response *httptest.ResponseRecorder, status int, copy string) {
				t.Helper()
				if response.Code != status || !strings.Contains(response.Body.String(), `lang="`+tc.language+`"`) || !strings.Contains(response.Body.String(), `dir="`+tc.direction+`"`) || !strings.Contains(response.Body.String(), copy) {
					t.Fatalf("status=%d page missing %q in %s", response.Code, copy, tc.language)
				}
			}
			resendCopy := "Request verification email"
			checkCopy := "Check your email"
			if tc.language == "fa" {
				resendCopy = "درخواست ایمیل تأیید"
				checkCopy = "ایمیل خود را بررسی کنید"
			}
			assertPage(call(http.MethodGet, "/verify/resend", nil, handler.ResendForm), http.StatusOK, resendCopy)
			known := call(http.MethodPost, "/verify/resend", url.Values{"email": {email}}, handler.ResendVerification)
			unknown := call(http.MethodPost, "/verify/resend", url.Values{"email": {"unknown@example.test"}}, handler.ResendVerification)
			assertPage(known, http.StatusOK, checkCopy)
			if known.Body.String() != unknown.Body.String() || strings.Contains(known.Body.String(), email) {
				t.Fatal("resend response disclosed account status")
			}
			pending := call(http.MethodGet, "/verify/pending", nil, func(w http.ResponseWriter, r *http.Request) {
				handler.Pending(w, r.WithContext(WithUser(r.Context(), registration.Account)))
			})
			assertPage(pending, http.StatusOK, tc.pending)
			if !strings.Contains(pending.Body.String(), `<bdi dir="ltr">`+email+`</bdi>`) {
				t.Fatal("pending page changed email direction or content")
			}
			assertPage(call(http.MethodGet, "/verify?token="+url.QueryEscape(token), nil, handler.VerifyForm), http.StatusOK, tc.verify)
			assertPage(call(http.MethodPost, "/verify", url.Values{"token": {token}, "password": {"wrong-password"}}, handler.Verify), http.StatusUnprocessableEntity, tc.passwordError)
			assertPage(call(http.MethodGet, "/verify/cancel?token="+url.QueryEscape(token), nil, handler.CancelForm), http.StatusOK, tc.cancel)
			assertPage(call(http.MethodPost, "/verify/cancel", url.Values{"token": {token}}, handler.Cancel), http.StatusOK, tc.cancelled)
			assertPage(call(http.MethodGet, "/verify?token="+url.QueryEscape(token), nil, handler.VerifyForm), http.StatusUnprocessableEntity, tc.expired)
		})
	}
}

func TestVerificationRequiresPasswordOutsideSignupBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, _ := testsupport.MigratedPostgres(t, ctx)
	queue := &capturedMailQueue{}
	accounts := NewAccountService(NewAccountRepository(pool, queue), NewService(dbgen.New(pool)), []byte("a-test-secret-that-is-long-enough-to-use"), "http://localhost:8080", accountCatalog(t))
	email := fmt.Sprintf("remote-%d@example.test", time.Now().UnixNano())
	registration, err := accounts.Register(ctx, email, "Remote", "correct-password-123", "fa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", registration.Account.ID)
	})
	message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	link, _ := url.Parse(strings.Fields(message.Body)[0])
	token := link.Query().Get("token")
	handler := NewHandler(NewService(dbgen.New(pool)), accounts, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	formResponse := httptest.NewRecorder()
	handler.VerifyForm(formResponse, withEnglishLocale(t, httptest.NewRequest(http.MethodGet, "/verify?token="+url.QueryEscape(token), nil)))
	if formResponse.Code != http.StatusOK || !strings.Contains(formResponse.Body.String(), "name=\"password\"") {
		t.Fatal("verification from another browser did not require the account password")
	}
	submit := func(password string) *httptest.ResponseRecorder {
		body := url.Values{"token": {token}, "password": {password}}
		request := withEnglishLocale(t, httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(body.Encode())))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		handler.Verify(response, request)
		return response
	}
	if response := submit("wrong-password"); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password status=%d", response.Code)
	}
	if response := submit("correct-password-123"); response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/account" {
		t.Fatalf("correct password status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
}

func withEnglishLocale(t *testing.T, request *http.Request) *http.Request {
	return withLanguageLocale(t, request, "en")
}

func withLanguageLocale(t *testing.T, request *http.Request, language string) *http.Request {
	t.Helper()
	return request.WithContext(accountCatalog(t).With(request.Context(), language, request.URL.RequestURI()))
}

func accountCatalog(t *testing.T) *locale.Catalog {
	t.Helper()
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
