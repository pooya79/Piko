package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web/request"
)

type contextKey struct{}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(contextKey{}).(User)
	return u, ok
}

const SessionCookie = "piko_session"
const CSRFCookie = "piko_csrf"

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
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
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
		case errors.Is(e, ErrAccountExists):
			field, key = "email", "auth.register.error.exists"
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
	if old, err := r.Cookie(SessionCookie); err == nil {
		_ = h.service.Logout(r.Context(), old.Value)
	}
	h.setSessionCookies(w, registration.Account, registration.Cookie, registration.CSRF, registration.ExpiresAt)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
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
	h.setSessionCookies(w, u, cookie, token, expires)
	return true
}
func (h *Handler) setSessionCookies(w http.ResponseWriter, u User, cookie, csrf string, expires time.Time) {
	locale.SetCookie(w, u.Language, h.secure)
	http.SetCookie(w, h.cookie(SessionCookie, cookie, expires, true))
	http.SetCookie(w, h.cookie(CSRFCookie, csrf, expires, false))
}
func (h *Handler) clearCookies(w http.ResponseWriter) {
	expired := time.Unix(1, 0)
	http.SetCookie(w, h.cookie(SessionCookie, "", expired, true))
	http.SetCookie(w, h.cookie(CSRFCookie, "", expired, false))
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
