package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"buildx/internal/locale"
	forms "buildx/internal/web/form"
	"buildx/internal/web/request"
	"github.com/a-h/templ"
)

type contextKey struct{}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(contextKey{}).(User)
	return u, ok
}

const SessionCookie = "buildx_session"
const CSRFCookie = "buildx_csrf"
const SignupCookie = "buildx_signup"

type Handler struct {
	service  *Service
	accounts *AccountService
	log      *slog.Logger
	secure   bool
}

func NewHandler(s *Service, accounts *AccountService, l *slog.Logger, secure bool) *Handler {
	return &Handler{service: s, accounts: accounts, log: l, secure: secure}
}
func (h *Handler) cookie(name, value string, expires time.Time, httpOnly bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: httpOnly, Secure: h.secure, SameSite: http.SameSiteLaxMode}
}
func renderLocalized(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if e := c.Render(r.Context(), w); e != nil {
		slog.ErrorContext(r.Context(), "render response", "error", e)
	}
}

// renderAccountError keeps account failures in the selected document language.
func renderAccountError(w http.ResponseWriter, r *http.Request, status int, key string) {
	renderLocalized(w, r, status, AccountErrorPage(request.CookieValue(r, CSRFCookie), locale.T(r.Context(), key)))
}

func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	renderLocalized(w, r, http.StatusOK, LoginPage(request.CookieValue(r, CSRFCookie), AccountForm{}))
}
func (h *Handler) RegisterForm(w http.ResponseWriter, r *http.Request) {
	renderLocalized(w, r, http.StatusOK, RegisterPage(request.CookieValue(r, CSRFCookie), AccountForm{}))
}
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		renderLocalized(w, r, http.StatusUnprocessableEntity, LoginPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.login.error.form"))))
		return
	}
	u, e := h.service.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if e != nil {
		if errors.Is(e, ErrInvalidCredentials) {
			renderLocalized(w, r, http.StatusUnprocessableEntity, LoginPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.login.error.credentials"))))
			return
		}
		h.log.ErrorContext(r.Context(), "login failed", "error", e)
		renderLocalized(w, r, http.StatusInternalServerError, LoginPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.login.error.unavailable"))))
		return
	}
	if !h.issueSession(w, r, u) {
		return
	}
	if u.Verified {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/verify/pending", http.StatusSeeOther)
	}
}
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if e := r.ParseForm(); e != nil {
		renderLocalized(w, r, http.StatusUnprocessableEntity, RegisterPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.login.error.form"))))
		return
	}
	registration, e := h.accounts.Register(r.Context(), r.FormValue("email"), r.FormValue("display_name"), r.FormValue("password"), locale.Language(r.Context()))
	if e != nil {
		form := accountForm(r, "")
		var field, key string
		switch {
		case errors.Is(e, ErrInvalidPassword):
			field, key = "password", "auth.register.error.password"
		case errors.Is(e, ErrInvalidEmail):
			field, key = "email", "auth.register.error.email"
		case errors.Is(e, ErrInvalidName):
			field, key = "display_name", "auth.register.error.name"
		default:
			h.log.ErrorContext(r.Context(), "registration failed", "error", e)
			form.Message = locale.T(r.Context(), "auth.register.error.unavailable")
			renderLocalized(w, r, http.StatusInternalServerError, RegisterPage(request.CookieValue(r, CSRFCookie), form))
			return
		}
		form.FieldErrors = map[string]string{field: locale.T(r.Context(), key)}
		renderLocalized(w, r, http.StatusUnprocessableEntity, RegisterPage(request.CookieValue(r, CSRFCookie), form))
		return
	}
	http.SetCookie(w, h.cookie(SignupCookie, registration.Receipt, time.Now().Add(signupReceiptLifetime), true))
	renderLocalized(w, r, http.StatusOK, CheckEmailPage(request.CookieValue(r, CSRFCookie)))
}
func (h *Handler) issueSession(w http.ResponseWriter, r *http.Request, u User) bool {
	if old, e := r.Cookie(SessionCookie); e == nil {
		_ = h.service.Logout(r.Context(), old.Value)
	}
	cookie, token, expires, e := h.service.NewSession(r.Context(), u.ID)
	if e != nil {
		h.log.ErrorContext(r.Context(), "create session", "error", e)
		renderLocalized(w, r, http.StatusInternalServerError, LoginPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.login.error.session"))))
		return false
	}
	locale.SetCookie(w, u.Language, h.secure)
	http.SetCookie(w, h.cookie(SessionCookie, cookie, expires, true))
	http.SetCookie(w, h.cookie(CSRFCookie, token, expires, false))
	return true
}
func (h *Handler) clearSignupCookie(w http.ResponseWriter) {
	http.SetCookie(w, h.cookie(SignupCookie, "", time.Unix(1, 0), true))
}
func (h *Handler) clearCookies(w http.ResponseWriter) {
	expired := time.Unix(1, 0)
	http.SetCookie(w, h.cookie(SessionCookie, "", expired, true))
	http.SetCookie(w, h.cookie(CSRFCookie, "", expired, false))
	h.clearSignupCookie(w)
}
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie(SessionCookie); e == nil {
		if e = h.service.Logout(r.Context(), c.Value); e != nil {
			h.log.WarnContext(r.Context(), "logout invalidation failed", "error", e)
		}
	}
	h.clearCookies(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (h *Handler) Pending(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		renderLocalized(w, r, 200, ResendPage(request.CookieValue(r, CSRFCookie), AccountForm{}))
		return
	}
	if u.Verified {
		http.Redirect(w, r, "/account", http.StatusSeeOther)
		return
	}
	renderLocalized(w, r, 200, PendingPage(u.Email, request.CookieValue(r, CSRFCookie)))
}
func (h *Handler) ResendForm(w http.ResponseWriter, r *http.Request) {
	if u, ok := UserFromContext(r.Context()); ok && !u.Verified {
		renderLocalized(w, r, 200, PendingPage(u.Email, request.CookieValue(r, CSRFCookie)))
		return
	}
	renderLocalized(w, r, 200, ResendPage(request.CookieValue(r, CSRFCookie), AccountForm{}))
}
func (h *Handler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderLocalized(w, r, http.StatusUnprocessableEntity, ResendPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.login.error.form"))))
		return
	}
	email := r.FormValue("email")
	if u, ok := UserFromContext(r.Context()); ok && !u.Verified {
		email = u.Email
	}
	if err := h.accounts.ResendVerification(r.Context(), email); err != nil {
		h.log.ErrorContext(r.Context(), "resend verification", "error", err)
		renderLocalized(w, r, http.StatusInternalServerError, ResendPage(request.CookieValue(r, CSRFCookie), accountForm(r, locale.T(r.Context(), "auth.resend.error.unavailable"))))
		return
	}
	renderLocalized(w, r, 200, CheckEmailPage(request.CookieValue(r, CSRFCookie)))
}

// verificationEvidence only extracts browser credentials; account rules live in AccountService.
func verificationEvidence(r *http.Request, password string) VerificationEvidence {
	evidence := VerificationEvidence{Password: password}
	if u, ok := UserFromContext(r.Context()); ok {
		evidence.Session = u
	}
	if cookie, err := r.Cookie(SignupCookie); err == nil {
		evidence.SignupReceipt = cookie.Value
	}
	return evidence
}
func (h *Handler) VerifyForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	info, requiresPassword, err := h.accounts.VerificationForm(r.Context(), token, verificationEvidence(r, ""))
	if errors.Is(err, ErrInvalidChallenge) {
		renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "load account link", "error", err)
		renderAccountError(w, r, http.StatusInternalServerError, "auth.link.error.unavailable")
		return
	}
	renderLocalized(w, r, 200, VerifyPage(token, info.Email, request.CookieValue(r, CSRFCookie), requiresPassword, ""))
}
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderAccountError(w, r, http.StatusUnprocessableEntity, "auth.login.error.form")
		return
	}
	token := r.FormValue("token")
	info, requiresPassword, err := h.accounts.VerificationForm(r.Context(), token, verificationEvidence(r, ""))
	if errors.Is(err, ErrInvalidChallenge) {
		renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "load account link", "error", err)
		renderAccountError(w, r, http.StatusInternalServerError, "auth.verify.error.unavailable")
		return
	}
	u, err := h.accounts.CompleteVerification(r.Context(), token, verificationEvidence(r, r.FormValue("password")))
	if err != nil {
		if errors.Is(err, ErrInvalidChallenge) {
			renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		} else if errors.Is(err, ErrInvalidCredentials) {
			renderLocalized(w, r, http.StatusUnprocessableEntity, VerifyPage(token, info.Email, request.CookieValue(r, CSRFCookie), true, locale.T(r.Context(), "auth.verify.error.password")))
		} else {
			h.log.ErrorContext(r.Context(), "verify account", "error", err)
			// Keep the already-loaded challenge context; an infrastructure error
			// does not identify a bad field or warrant echoing the password.
			renderLocalized(w, r, http.StatusInternalServerError, VerifyPage(token, info.Email, request.CookieValue(r, CSRFCookie), requiresPassword, locale.T(r.Context(), "auth.verify.error.unavailable")))
		}
		return
	}
	h.clearSignupCookie(w)
	if !h.issueSession(w, r, u) {
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (h *Handler) CancelForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	info, err := h.accounts.Challenge(r.Context(), token)
	if err != nil && !errors.Is(err, ErrInvalidChallenge) {
		h.log.ErrorContext(r.Context(), "load account link", "error", err)
		renderAccountError(w, r, http.StatusInternalServerError, "auth.link.error.unavailable")
		return
	}
	if err != nil || info.Purpose != "verify" {
		renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		return
	}
	renderLocalized(w, r, 200, CancelPage(token, request.CookieValue(r, CSRFCookie)))
}
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderAccountError(w, r, http.StatusUnprocessableEntity, "auth.login.error.form")
		return
	}
	token := r.FormValue("token")
	info, err := h.accounts.Challenge(r.Context(), token)
	if err != nil || info.Purpose != "verify" {
		renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		return
	}
	if err := h.accounts.CancelPending(r.Context(), token); err != nil {
		if errors.Is(err, ErrInvalidChallenge) {
			renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		} else {
			h.log.ErrorContext(r.Context(), "cancel pending account", "error", err)
			renderAccountError(w, r, http.StatusInternalServerError, "auth.cancel.error.unavailable")
		}
		return
	}
	if u, ok := UserFromContext(r.Context()); ok && u.ID == info.UserID {
		h.clearCookies(w)
	} else {
		h.clearSignupCookie(w)
	}
	renderLocalized(w, r, 200, CancelledPage())
}
func (h *Handler) ForgotForm(w http.ResponseWriter, r *http.Request) {
	renderLocalized(w, r, 200, ForgotPage(request.CookieValue(r, CSRFCookie), false, AccountForm{}))
}
func (h *Handler) Forgot(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderLocalized(w, r, http.StatusUnprocessableEntity, ForgotPage(request.CookieValue(r, CSRFCookie), false, accountForm(r, locale.T(r.Context(), "auth.login.error.form"))))
		return
	}
	if err := h.accounts.RequestReset(r.Context(), r.FormValue("email")); err != nil {
		h.log.ErrorContext(r.Context(), "request recovery", "error", err)
		renderLocalized(w, r, http.StatusInternalServerError, ForgotPage(request.CookieValue(r, CSRFCookie), false, accountForm(r, locale.T(r.Context(), "auth.recovery.error.request"))))
		return
	}
	renderLocalized(w, r, 200, ForgotPage(request.CookieValue(r, CSRFCookie), true, AccountForm{}))
}
func (h *Handler) ResetForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	info, err := h.accounts.Challenge(r.Context(), token)
	if err != nil && !errors.Is(err, ErrInvalidChallenge) {
		h.log.ErrorContext(r.Context(), "load account link", "error", err)
		renderAccountError(w, r, http.StatusInternalServerError, "auth.link.error.unavailable")
		return
	}
	if err != nil || info.Purpose != "reset" {
		renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		return
	}
	renderLocalized(w, r, 200, ResetPage(token, request.CookieValue(r, CSRFCookie), forms.Feedback{}))
}
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderAccountError(w, r, http.StatusUnprocessableEntity, "auth.login.error.form")
		return
	}
	token := r.FormValue("token")
	err := h.accounts.Reset(r.Context(), token, r.FormValue("password"))
	if err != nil {
		if errors.Is(err, ErrInvalidChallenge) {
			renderLocalized(w, r, http.StatusUnprocessableEntity, LinkExpiredPage())
		} else if errors.Is(err, ErrInvalidPassword) {
			renderLocalized(w, r, http.StatusUnprocessableEntity, ResetPage(token, request.CookieValue(r, CSRFCookie), forms.Feedback{FieldErrors: map[string]string{"password": locale.T(r.Context(), "auth.reset.error.password")}}))
		} else {
			h.log.ErrorContext(r.Context(), "reset password", "error", err)
			renderLocalized(w, r, http.StatusInternalServerError, ResetPage(token, request.CookieValue(r, CSRFCookie), forms.Feedback{Message: locale.T(r.Context(), "auth.reset.error.unavailable")}))
		}
		return
	}
	h.clearCookies(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	renderLocalized(w, r, 200, ProfilePage(u, request.CookieValue(r, CSRFCookie)))
}

// SaveLanguage persists a signed-in choice before the locale handler updates its cookie.
func (h *Handler) SaveLanguage(ctx context.Context, language string) error {
	u, ok := UserFromContext(ctx)
	if !ok {
		return nil
	}
	return h.service.SetLanguage(ctx, u.ID, language)
}
