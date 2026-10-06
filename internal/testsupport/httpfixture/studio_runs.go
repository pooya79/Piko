package httpfixture

import (
	"net/http/httptest"
	"net/url"
	"strings"
)

func StudioRequest(b *Browser, method, path string, values url.Values) *httptest.ResponseRecorder {
	if values == nil {
		values = url.Values{}
	}
	if method == "POST" && !values.Has("csrf_token") {
		values.Set("csrf_token", b.Cookie("piko_csrf"))
	}
	r := httptest.NewRequest(method, path, strings.NewReader(values.Encode()))
	r.Header.Set("X-Piko-Studio", "fragment")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range b.Jar.Cookies(b.Base) {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	b.Router.ServeHTTP(w, r)
	return w
}
