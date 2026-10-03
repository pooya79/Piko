package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	webx "github.com/pooya79/Piko/internal/web"
	"golang.org/x/net/html"
)

func testLocaleCatalog(t *testing.T) *locale.Catalog {
	t.Helper()
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// Password guidance belongs to the input description, leaving its name concise.
func TestRegistrationPasswordGuidance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := testLocaleCatalog(t)
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: catalog}
	router := buildRouter(nil, mw, nil, auth.NewHandler(nil, nil, logger, false), testBotService(t, nil))
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/register", nil)
			r.AddCookie(&http.Cookie{Name: "piko_language", Value: language})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			doc, err := html.Parse(strings.NewReader(w.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			var input, label, helper *html.Node
			var visit func(*html.Node)
			visit = func(n *html.Node) {
				for _, a := range n.Attr {
					if a.Key == "id" && a.Val == "password" {
						input = n
					}
					if a.Key == "for" && a.Val == "password" {
						label = n
					}
					if a.Key == "id" && a.Val == "password-hint" {
						helper = n
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					visit(c)
				}
			}
			visit(doc)
			if input == nil || label == nil || helper == nil {
				t.Fatal("password label, input, or guidance missing")
			}
			described := false
			for _, a := range input.Attr {
				if a.Key == "aria-describedby" && a.Val == "password-hint" {
					described = true
				}
			}
			if !described || helper.Parent != input.Parent || helper.PrevSibling != input {
				t.Error("guidance must follow and describe the password input")
			}
			if label.LastChild == nil || label.LastChild.Type != html.TextNode {
				t.Error("password label contains helper markup")
			}
		})
	}
}

func TestLegacyLanguageCookieCannotRestoreEnglish(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: testLocaleCatalog(t)}
	router := buildRouter(nil, mw, nil, auth.NewHandler(nil, nil, logger, false), testBotService(t, nil))
	for _, path := range []string{"/login", "/register", "/missing"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: "piko_language", Value: "en"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		body := w.Body.String()
		if !strings.Contains(body, `lang="fa"`) || !strings.Contains(body, `dir="rtl"`) || strings.Contains(body, `name="language"`) {
			t.Errorf("%s allowed language selection or English rendering", path)
		}
	}
}

func TestUnsupportedMethodUsesPersianSharedFeedback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: testLocaleCatalog(t)}
	router := buildRouter(nil, mw, nil, auth.NewHandler(nil, nil, logger, false), testBotService(t, nil))
	r := httptest.NewRequest(http.MethodGet, "/logout", nil)
	r.AddCookie(&http.Cookie{Name: "piko_language", Value: "en"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("method rejection status=%d allow=%q", w.Code, w.Header().Get("Allow"))
	}
	for _, want := range []string{`lang="fa"`, `dir="rtl"`, "روش درخواست برای این صفحه مجاز نیست."} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("method rejection missing %q", want)
		}
	}
}
