package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web/request"
)

type Middleware struct {
	Auth          SessionService
	LocaleCatalog *locale.Catalog
	Log           *slog.Logger
	SecureCookie  bool
	TrustedProxy  bool
	Secret        []byte
}
type SessionService interface {
	LoadSession(context.Context, string) (auth.Session, error)
	VerifyCSRF(context.Context, string, string) bool
	RenewCSRF(context.Context, string) (string, error)
}

// RequestLocale makes Persian copy available to all renderers.
func (m Middleware) RequestLocale(next http.Handler) http.Handler {
	if m.LocaleCatalog == nil {
		panic("locale catalog was not injected")
	}
	return m.LocaleCatalog.Middleware(next)
}

type requestIDKey struct{}

func RequestID(ctx context.Context) string { v, _ := ctx.Value(requestIDKey{}).(string); return v }
func randomID() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (m Middleware) signedCSRF() string {
	payload := randomID()
	mac := hmac.New(sha256.New, m.Secret)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (m Middleware) validSignedCSRF(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, m.Secret)
	_, _ = mac.Write([]byte(parts[0]))
	return hmac.Equal(got, mac.Sum(nil))
}
func (m Middleware) RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := randomID()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
func (m Middleware) Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				m.Log.ErrorContext(r.Context(), "panic recovered", "panic", v, "stack", string(debug.Stack()), "request_id", RequestID(r.Context()))
				if m.LocaleCatalog != nil {
					r = r.WithContext(m.LocaleCatalog.With(r.Context()))
				}
				_, signedIn := auth.UserFromContext(r.Context())
				if m.LocaleCatalog != nil || signedIn {
					RenderError(w, r, http.StatusInternalServerError, "error.message.server")
				} else {
					http.Error(w, "خطایی در سرور رخ داد.", http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(s int) { w.status = s; w.ResponseWriter.WriteHeader(s) }
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, e
}
func (m Middleware) Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		m.Log.InfoContext(r.Context(), "http request", "request_id", RequestID(r.Context()), "method", r.Method, "route", r.URL.Path, "status", sw.status, "bytes", sw.bytes, "duration_ms", time.Since(start).Milliseconds())
	})
}
func (m Middleware) Security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
func (m Middleware) LimitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}
func (m Middleware) Session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(auth.SessionCookie)
		if e == nil {
			if s, e := m.Auth.LoadSession(r.Context(), c.Value); e == nil {
				r = r.WithContext(auth.WithUser(r.Context(), s.User))
			} else if errors.Is(e, auth.ErrSessionNotFound) {
				http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.SecureCookie, SameSite: http.SameSiteLaxMode})
			} else {
				m.Log.WarnContext(r.Context(), "session load failed", "error", e)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := auth.UserFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRF uses session-bound tokens for signed-in users and signed double-submit tokens
// for anonymous forms. Unsafe requests must present the token in both cookie and form.
func (m Middleware) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			cookie, err := r.Cookie(auth.CSRFCookie)
			if _, authenticated := auth.UserFromContext(r.Context()); authenticated {
				session, sessionErr := r.Cookie(auth.SessionCookie)
				if sessionErr != nil {
					RenderError(w, r, http.StatusServiceUnavailable, "error.message.session.unavailable")
					return
				}
				if err != nil || !m.Auth.VerifyCSRF(r.Context(), session.Value, cookie.Value) {
					token, renewErr := m.Auth.RenewCSRF(r.Context(), session.Value)
					if renewErr != nil {
						RenderError(w, r, http.StatusServiceUnavailable, "error.message.session.unavailable")
						return
					}
					m.setCSRFCookie(w, r, token)
				}
			} else if err != nil || !m.validSignedCSRF(cookie.Value) {
				m.setCSRFCookie(w, r, m.signedCSRF())
			}
			next.ServeHTTP(w, r)
			return
		}
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			_ = r.ParseForm()
		}
		cookie, e := r.Cookie(auth.CSRFCookie)
		if e != nil || r.FormValue("csrf_token") == "" || cookie.Value != r.FormValue("csrf_token") {
			RenderError(w, r, http.StatusForbidden, "error.message.csrf")
			return
		}
		if _, authenticated := auth.UserFromContext(r.Context()); authenticated {
			sc, e := r.Cookie(auth.SessionCookie)
			if e != nil || !m.Auth.VerifyCSRF(r.Context(), sc.Value, cookie.Value) {
				RenderError(w, r, http.StatusForbidden, "error.message.csrf")
				return
			}
		} else if !m.validSignedCSRF(cookie.Value) {
			RenderError(w, r, http.StatusForbidden, "error.message.csrf")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setCSRFCookie also updates the request so a form rendered now sees the renewed token.
func (m Middleware) setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: auth.CSRFCookie, Value: token, Path: "/", HttpOnly: false, Secure: m.SecureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != auth.CSRFCookie {
			r.AddCookie(c)
		}
	}
	r.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: token})
}

// ClientIP uses X-Forwarded-For only when TrustedProxy is enabled. That setting
// assumes the proxy overwrites the header before forwarding the request.
func (m Middleware) ClientIP(r *http.Request) string {
	if m.TrustedProxy {
		if v := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(v) != nil {
			return v
		}
	}
	host, _, e := net.SplitHostPort(r.RemoteAddr)
	if e == nil {
		return host
	}
	return r.RemoteAddr
}
func RenderError(w http.ResponseWriter, r *http.Request, status int, messageKey string) {
	displayName := ""
	// Public account forms use their own layout even for a signed-in browser.
	accountPath := r.URL.Path == "/login" || r.URL.Path == "/register"
	if user, ok := auth.UserFromContext(r.Context()); ok && !accountPath {
		displayName = user.DisplayName
	}
	message := locale.T(r.Context(), messageKey)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if e := ErrorPage(status, message, displayName, request.CookieValue(r, auth.CSRFCookie)).Render(r.Context(), w); e != nil {
		slog.ErrorContext(r.Context(), "render error page", "error", e)
	}
}
