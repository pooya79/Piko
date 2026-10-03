package request

import "net/http"

// CookieValue returns an empty value when a request does not carry the named cookie.
func CookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
