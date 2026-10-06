package httpfixture

import (
	"net/url"
	"testing"
)

func StartPikoChat(t *testing.T, b *Browser) string {
	t.Helper()
	got := b.Post("/chats", url.Values{})
	if got.Code != 303 {
		t.Fatalf("start Piko chat: %d", got.Code)
	}
	return got.Header().Get("Location")
}
