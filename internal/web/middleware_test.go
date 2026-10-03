package web

import (
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRFRejectsMissingToken(t *testing.T) {
	m := Middleware{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := m.CSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler called") }))
	r := httptest.NewRequest("POST", "/account", strings.NewReader("name=x"))
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithContext(catalog.With(r.Context(), "fa", r.URL.RequestURI()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}
func TestCSRFFirstGETMakesTokenAvailableToHandler(t *testing.T) {
	m := Middleware{Secret: []byte("test-secret")}
	h := m.CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CSRFCookie)
		if err != nil || c.Value == "" {
			t.Fatal("csrf token unavailable during first render")
		}
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/register", nil))
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("csrf cookie was not set")
	}
}
func TestAnonymousProtectedRouteRedirects(t *testing.T) {
	m := Middleware{}
	h := m.RequireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler called") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/account", nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", w.Code)
	}
}
func TestAccountCanReachProtectedRoute(t *testing.T) {
	m := Middleware{}
	h := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: 7, Email: "pending@example.test"}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d location=%q", w.Code, w.Header().Get("Location"))
	}
}

func TestAuthenticatedErrorKeepsAccountNavigation(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r = r.WithContext(auth.WithUser(catalog.With(r.Context(), "en", r.URL.RequestURI()), auth.User{ID: 7, DisplayName: "Mina"}))
	w := httptest.NewRecorder()
	RenderError(w, r, http.StatusBadRequest, "Invalid CSRF token.")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
	for _, want := range []string{"Invalid CSRF token.", "Mina", "/account", "/logout"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("error page missing %q", want)
		}
	}
}

func TestAuthenticatedErrorUsesPersianShellAndFeedback(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r = r.WithContext(auth.WithUser(catalog.With(r.Context(), "fa", r.URL.RequestURI()), auth.User{ID: 7, DisplayName: "Mina", Language: "fa"}))
	w := httptest.NewRecorder()
	RenderError(w, r, http.StatusBadRequest, "Invalid CSRF token.")
	for _, want := range []string{`lang="fa" dir="rtl"`, "نشست یا کد امنیتی فرم نامعتبر است.", "خطا", "۴۰۰", "خروج", "Mina"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("error page missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "Invalid CSRF token.") {
		t.Error("English feedback leaked into Persian error")
	}
}

func TestAuthenticatedPanicUsesLocalizedError(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r = r.WithContext(auth.WithUser(catalog.With(r.Context(), "fa", "/account"), auth.User{ID: 7, DisplayName: "Mina", Language: "fa"}))
	m := Middleware{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := httptest.NewRecorder()
	m.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("failure") })).ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "خطایی در سرور رخ داد.") || !strings.Contains(w.Body.String(), `lang="fa" dir="rtl"`) {
		t.Fatalf("localized panic status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSignedInAccountSeesLocalizedPublicAccountError(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/register", nil)
	ctx := catalog.With(request.Context(), "fa", request.URL.RequestURI())
	request = request.WithContext(auth.WithUser(ctx, auth.User{ID: 7, DisplayName: "Mina", Language: "fa"}))
	response := httptest.NewRecorder()
	RenderError(response, request, http.StatusForbidden, "Invalid CSRF token.")
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `lang="fa"`) || !strings.Contains(response.Body.String(), `dir="rtl"`) || !strings.Contains(response.Body.String(), "نشست یا کد امنیتی فرم نامعتبر است.") {
		t.Fatalf("signed-in account public error status=%d or language missing", response.Code)
	}
}

func TestSharedErrorFollowsRequestLanguage(t *testing.T) {
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	m := Middleware{Secret: []byte("test-secret")}
	handler := catalog.Middleware(m.CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RenderError(w, r, http.StatusBadRequest, "Invalid CSRF token.")
	})))
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		wants  []string
	}{
		{name: "first visit", wants: []string{`lang="fa"`, `dir="rtl"`, "نشست یا کد امنیتی فرم نامعتبر است.", `name="language"`}},
		{name: "English choice", cookie: &http.Cookie{Name: locale.CookieName, Value: "en"}, wants: []string{`lang="en"`, `dir="ltr"`, "Invalid CSRF token."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/bad", nil)
			if tc.cookie != nil {
				request.AddCookie(tc.cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d", response.Code)
			}
			for _, want := range tc.wants {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("error page missing %q", want)
				}
			}
		})
	}
}
