package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	webx "github.com/pooya79/Piko/internal/web"
	"golang.org/x/net/html"
)

func TestPublicLoginDefaultsToPersian(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: catalog}
	h := auth.NewHandler(nil, nil, logger, false)
	router := buildRouter(nil, mw, nil, h)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	for _, want := range []string{`lang="fa"`, `dir="rtl"`, "خوش آمدید", `name="language"`, `value="en"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("login missing %q", want)
		}
	}
	badCSRF := httptest.NewRecorder()
	router.ServeHTTP(badCSRF, httptest.NewRequest(http.MethodPost, "/register", strings.NewReader("email=someone%40example.test")))
	if badCSRF.Code != http.StatusForbidden || !strings.Contains(badCSRF.Body.String(), `lang="fa"`) || !strings.Contains(badCSRF.Body.String(), `dir="rtl"`) || !strings.Contains(badCSRF.Body.String(), "نشست یا کد امنیتی فرم نامعتبر است.") {
		t.Fatalf("Persian CSRF feedback status=%d", badCSRF.Code)
	}
	for _, page := range []struct{ path, copy string }{
		{"/register", "حساب خود را بسازید."},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, page.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `lang="fa"`) || !strings.Contains(response.Body.String(), `dir="rtl"`) || !strings.Contains(response.Body.String(), page.copy) {
			t.Errorf("%s did not render in Persian: status=%d", page.path, response.Code)
		}
	}
}

func TestPublicLanguageSwitchIsSafeAndPersistent(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), SecureCookie: true, LocaleCatalog: catalog}
	router := buildRouter(nil, mw, nil, auth.NewHandler(nil, nil, logger, true))
	send := func(method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var body *strings.Reader
		if form == nil {
			body = strings.NewReader("")
		} else {
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	first := send(http.MethodGet, "/login?from=menu", nil)
	var csrf *http.Cookie
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == auth.CSRFCookie {
			csrf = cookie
		}
	}
	if csrf == nil {
		t.Fatal("login omitted CSRF cookie")
	}
	if got := send(http.MethodPost, "/language", url.Values{"language": {"en"}, "return_to": {"/login"}}); got.Code != http.StatusForbidden {
		t.Fatalf("switch without CSRF status=%d", got.Code)
	}
	switchTo := func(language, destination string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"csrf_token": {csrf.Value}, "language": {language}, "return_to": {destination}}
		return send(http.MethodPost, "/language", form, append(cookies, csrf)...)
	}
	en := switchTo("en", "/login?from=menu")
	if en.Code != http.StatusSeeOther || en.Header().Get("Location") != "/login?from=menu" {
		t.Fatalf("English switch status=%d location=%q", en.Code, en.Header().Get("Location"))
	}
	var languageCookie *http.Cookie
	for _, cookie := range en.Result().Cookies() {
		if cookie.Name == locale.CookieName {
			languageCookie = cookie
		}
	}
	if languageCookie == nil || languageCookie.Value != "en" || !languageCookie.Secure || !languageCookie.HttpOnly || languageCookie.SameSite != http.SameSiteLaxMode || languageCookie.MaxAge <= 0 {
		t.Fatalf("language cookie=%+v", languageCookie)
	}
	englishPage := send(http.MethodGet, "/login", nil, languageCookie)
	for _, want := range []string{`lang="en"`, `dir="ltr"`, "Welcome back."} {
		if !strings.Contains(englishPage.Body.String(), want) {
			t.Errorf("English page missing %q", want)
		}
	}
	badEnglishCSRF := send(http.MethodPost, "/register", url.Values{"email": {"someone@example.test"}}, languageCookie)
	if badEnglishCSRF.Code != http.StatusForbidden || !strings.Contains(badEnglishCSRF.Body.String(), `lang="en"`) || !strings.Contains(badEnglishCSRF.Body.String(), `dir="ltr"`) || !strings.Contains(badEnglishCSRF.Body.String(), "Invalid CSRF token.") {
		t.Fatalf("English CSRF feedback status=%d", badEnglishCSRF.Code)
	}
	for _, page := range []struct{ path, copy string }{
		{"/register", "Create your account."},
	} {
		response := send(http.MethodGet, page.path, nil, languageCookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `lang="en"`) || !strings.Contains(response.Body.String(), `dir="ltr"`) || !strings.Contains(response.Body.String(), page.copy) {
			t.Errorf("%s did not render in English: status=%d", page.path, response.Code)
		}
	}
	if bad := switchTo("de", "/login", languageCookie); bad.Code != http.StatusBadRequest || len(bad.Result().Cookies()) != 0 {
		t.Fatalf("unsupported language status=%d cookies=%v", bad.Code, bad.Result().Cookies())
	}
	for _, destination := range []string{"https://evil.example/", "//evil.example/", "/%2fevil.example/", "/%5cevil.example/"} {
		got := switchTo("en", destination)
		if got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/login" {
			t.Errorf("unsafe destination %q redirected to %q", destination, got.Header().Get("Location"))
		}
	}
	fa := switchTo("fa", "/login", languageCookie)
	var persianCookie *http.Cookie
	for _, cookie := range fa.Result().Cookies() {
		if cookie.Name == locale.CookieName {
			persianCookie = cookie
		}
	}
	if persianCookie == nil || persianCookie.Value != "fa" {
		t.Fatalf("Persian cookie=%+v", persianCookie)
	}
	if page := send(http.MethodGet, "/login", nil, persianCookie); !strings.Contains(page.Body.String(), `lang="fa"`) || !strings.Contains(page.Body.String(), "خوش آمدید") {
		t.Fatal("Persian choice did not persist")
	}
}

func testLocaleCatalog(t *testing.T) *locale.Catalog {
	t.Helper()
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func TestSignedInLanguageSwitchFailureUsesLocalizedFeedback(t *testing.T) {
	catalog := testLocaleCatalog(t)
	handler := locale.Handler{
		SaveAccountLanguage: func(context.Context, string) error { return errors.New("storage unavailable") },
		ShowError:           webx.RenderError,
	}
	for _, tc := range []struct{ language, message string }{
		{"fa", "تغییر زبان اکنون ممکن نیست."},
		{"en", "Unable to change language."},
	} {
		r := httptest.NewRequest(http.MethodPost, "/language", strings.NewReader("language=en"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(auth.WithUser(catalog.With(r.Context(), tc.language, "/account"), auth.User{ID: 7, DisplayName: "Mina", Language: tc.language}))
		w := httptest.NewRecorder()
		handler.Switch(w, r)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), tc.message) || !strings.Contains(w.Body.String(), `lang="`+tc.language+`"`) || len(w.Result().Cookies()) != 0 {
			t.Errorf("%s failure status=%d, localized=%v, cookies=%v", tc.language, w.Code, strings.Contains(w.Body.String(), tc.message), w.Result().Cookies())
		}
	}
	unsupported := httptest.NewRequest(http.MethodPost, "/language", strings.NewReader("language=de"))
	unsupported.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unsupported = unsupported.WithContext(auth.WithUser(catalog.With(unsupported.Context(), "fa", "/account"), auth.User{ID: 7, DisplayName: "Mina", Language: "fa"}))
	response := httptest.NewRecorder()
	handler.Switch(response, unsupported)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "زبان انتخاب\u200cشده پشتیبانی نمی\u200cشود.") || len(response.Result().Cookies()) != 0 {
		t.Errorf("unsupported signed-in switch status=%d body=%s", response.Code, response.Body.String())
	}
}

// The Latin brand must declare its own direction on every public route.
func TestPublicWordmarkKeepsLatinDirection(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: testLocaleCatalog(t)}
	router := buildRouter(nil, mw, nil, auth.NewHandler(nil, nil, logger, false))
	for _, language := range []string{"fa", "en"} {
		for _, path := range []string{"/login", "/register"} {
			t.Run(language+path, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.AddCookie(&http.Cookie{Name: locale.CookieName, Value: language})
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				doc, err := html.Parse(strings.NewReader(w.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				found := false
				var visit func(*html.Node)
				visit = func(node *html.Node) {
					if node.Type == html.ElementNode && node.Data == "a" && node.FirstChild != nil && node.FirstChild.Data == "Piko" {
						found = true
						attributes := map[string]string{}
						for _, attr := range node.Attr {
							attributes[attr.Key] = attr.Val
						}
						if attributes["dir"] != "ltr" || attributes["href"] != "/" {
							t.Error("home wordmark lost Latin direction or its destination")
						}
					}
					for child := node.FirstChild; child != nil; child = child.NextSibling {
						visit(child)
					}
				}
				visit(doc)
				if !found {
					t.Error("home wordmark missing")
				}
			})
		}
	}
}

// Password guidance belongs to the input description, leaving its name concise.
func TestRegistrationPasswordGuidance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := testLocaleCatalog(t)
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: catalog}
	router := buildRouter(nil, mw, nil, auth.NewHandler(nil, nil, logger, false))
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/register", nil)
			r.AddCookie(&http.Cookie{Name: locale.CookieName, Value: language})
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
