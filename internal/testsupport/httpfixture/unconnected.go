package httpfixture

import (
	"net/http"
	"testing"
)

func UnconnectedFixture(t *testing.T) (*HTTP, *Browser) {
	t.Helper()
	a, b, _ := BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Unconnected Bot work contacted Telegram")
		w.WriteHeader(500)
	})
	return a, b
}
