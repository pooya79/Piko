package locale

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

type Handler struct {
	SecureCookie        bool
	SaveAccountLanguage func(context.Context, string) error
	// ShowError is injected by web so language failures use its shared page.
	ShowError func(http.ResponseWriter, *http.Request, int, string)
}

// SetCookie keeps the browser choice aligned with an account after authentication.
func SetCookie(w http.ResponseWriter, language string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: language, Path: "/", MaxAge: 365 * 24 * 60 * 60, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

// Switch is protected by the application's CSRF middleware like every POST action.
func (h Handler) Switch(w http.ResponseWriter, r *http.Request) {
	lang := r.FormValue("language")
	if !Supported(lang) {
		h.fail(w, r, http.StatusBadRequest, "Unsupported language.")
		return
	}
	if h.SaveAccountLanguage != nil {
		if err := h.SaveAccountLanguage(r.Context(), lang); err != nil {
			slog.ErrorContext(r.Context(), "save account language", "error", err)
			h.fail(w, r, http.StatusInternalServerError, "Unable to change language.")
			return
		}
	}
	SetCookie(w, lang, h.SecureCookie)
	http.Redirect(w, r, safeReturn(r.FormValue("return_to")), http.StatusSeeOther)
}

func (h Handler) fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	if h.ShowError != nil {
		h.ShowError(w, r, status, message)
		return
	}
	http.Error(w, message, status)
}

// safeReturn accepts only one-rooted local paths. Encoded slashes and backslashes
// are checked after URL parsing because browsers may treat them as an authority.
func safeReturn(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "\\\r\n\t") {
		return "/login"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") || strings.ContainsAny(parsed.Path, "\\\r\n\t") {
		return "/login"
	}
	return value
}
