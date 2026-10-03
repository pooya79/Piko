package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"golang.org/x/net/html"
)

// Validation-only submissions stop before persistence, using the real services
// and the same locale/CSRF route boundary as the application.
func accountFeedbackRouter(t *testing.T) http.Handler {
	t.Helper()
	catalog := testLocaleCatalog(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := web.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: catalog}
	h := auth.NewHandler(auth.NewService(nil), auth.NewAccountService(nil, nil), logger, false)
	r := chi.NewRouter()
	r.Use(mw.RequestLocale, mw.CSRF)
	r.Get("/register", h.RegisterForm)
	r.Post("/register", h.Register)
	r.Get("/login", h.LoginForm)
	r.Post("/login", h.Login)
	return r
}

func feedbackSubmission(t *testing.T, router http.Handler, language, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/register", nil))
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	for _, c := range first.Result().Cookies() {
		if c.Name == auth.CSRFCookie {
			form.Set("csrf_token", c.Value)
			r = httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
			r.AddCookie(c)
			break
		}
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "piko_language", Value: language})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func feedbackElements(t *testing.T, body string) map[string]*html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*html.Node{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if id := feedbackAttr(n, "id"); id != "" {
			nodes[id] = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	return nodes
}

func feedbackAttr(n *html.Node, key string) string {
	if n != nil {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
	}
	return ""
}

func TestRegistrationFeedbackRetainsInputAndIdentifiesField(t *testing.T) {
	router := accountFeedbackRouter(t)
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			form := url.Values{"email": {"mina@example.test"}, "display_name": {"Mina مینا"}, "password": {"short"}}
			w := feedbackSubmission(t, router, language, "/register", form)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d", w.Code)
			}
			nodes := feedbackElements(t, w.Body.String())
			if feedbackAttr(nodes["email"], "value") != "mina@example.test" || feedbackAttr(nodes["display-name"], "value") != "Mina مینا" {
				t.Error("non-sensitive input was lost")
			}
			if feedbackAttr(nodes["password"], "value") != "" {
				t.Error("password was repopulated")
			}
			if feedbackAttr(nodes["password"], "aria-invalid") != "true" || feedbackAttr(nodes["password"], "aria-describedby") != "password-hint password-error" || nodes["password-error"] == nil {
				t.Error("password error does not describe the invalid field alongside its hint")
			}
			ctx := testLocaleCatalog(t).With(t.Context())
			if !strings.Contains(w.Body.String(), locale.T(ctx, "auth.register.error.password")) {
				t.Error("localized error missing")
			}
		})
	}
}

func TestAccountFeedbackKeepsSensitiveFailuresGeneric(t *testing.T) {
	router := accountFeedbackRouter(t)
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			w := feedbackSubmission(t, router, language, "/login", url.Values{"email": {"invalid-address"}, "password": {"private-password"}})
			nodes := feedbackElements(t, w.Body.String())
			if w.Code != 422 || feedbackAttr(nodes["email"], "value") != "invalid-address" {
				t.Error("login lost email or validation status")
			}
			if strings.Contains(w.Body.String(), "private-password") || strings.Contains(w.Body.String(), `aria-invalid="true"`) || !strings.Contains(w.Body.String(), `role="alert"`) {
				t.Error("credentials must stay secret with generic form feedback")
			}
		})
	}
}

func TestRegistrationDetailsIdentifyOnlyKnownInvalidField(t *testing.T) {
	router := accountFeedbackRouter(t)
	for _, language := range []string{"en", "fa"} {
		for _, tc := range []struct{ field, id, value, key string }{
			{"email", "email", "invalid-address", "auth.register.error.email"},
			{"display_name", "display-name", "   ", "auth.register.error.name"},
			{"display_name", "display-name", strings.Repeat("م", 81), "auth.register.error.name"},
		} {
			t.Run(language+tc.field, func(t *testing.T) {
				form := url.Values{"email": {"mina@example.test"}, "display_name": {"Mina مینا"}, "password": {"private-password-123"}}
				form.Set(tc.field, tc.value)
				w := feedbackSubmission(t, router, language, "/register", form)
				nodes := feedbackElements(t, w.Body.String())
				if w.Code != 422 || feedbackAttr(nodes[tc.id], "aria-invalid") != "true" || feedbackAttr(nodes[tc.id], "aria-describedby") != tc.id+"-error" {
					t.Error("known validation did not identify its field")
				}
				ctx := testLocaleCatalog(t).With(t.Context())
				if !strings.Contains(w.Body.String(), locale.T(ctx, tc.key)) || strings.Contains(w.Body.String(), "private-password-123") {
					t.Error("localized feedback missing or password echoed")
				}
				for _, id := range []string{"display-name", "email", "password"} {
					if id != tc.id && feedbackAttr(nodes[id], "aria-invalid") == "true" {
						t.Errorf("unrelated field %s marked invalid", id)
					}
				}
			})
		}
	}
}
