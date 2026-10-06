package httpfixture

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func PreviewRequest(b *Browser, method, path string, values url.Values) *httptest.ResponseRecorder {
	if values == nil {
		values = url.Values{}
	}
	if method == "POST" && !values.Has("csrf_token") {
		values.Set("csrf_token", b.Cookie("piko_csrf"))
	}
	r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	r.Header.Set("X-Piko-Preview", "fragment")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range b.Jar.Cookies(b.Base) {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	b.Router.ServeHTTP(w, r)
	return w
}

// Migration fixtures model historical snapshots whose source was never tracked.
func StartLegacyPreview(t *testing.T, a *HTTP, b *Browser) string {
	t.Helper()
	got := b.Post("/bots/1/preview", url.Values{})
	if got.Code != 303 {
		t.Fatalf("legacy Preview setup: %d", got.Code)
	}
	if _, err := a.DB.Exec("UPDATE bot_previews SET source_revision = 0"); err != nil {
		t.Fatal(err)
	}
	return got.Header().Get("Location")
}
