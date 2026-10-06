package httpfixture

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
	"github.com/pooya79/Piko/internal/web"
	"golang.org/x/net/html"
)

// Validation-only submissions stop before persistence, using the real services
// and the same locale/CSRF route boundary as the application.
func AccountFeedbackRouter(t *testing.T) http.Handler {
	t.Helper()
	catalog := TestLocaleCatalog(t)
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

func FeedbackSubmission(t *testing.T, router http.Handler, language, path string, form url.Values) *httptest.ResponseRecorder {
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

func FeedbackElements(t *testing.T, body string) map[string]*html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*html.Node{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if id := FeedbackAttr(n, "id"); id != "" {
			nodes[id] = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	return nodes
}

func FeedbackAttr(n *html.Node, key string) string {
	if n != nil {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
	}
	return ""
}
