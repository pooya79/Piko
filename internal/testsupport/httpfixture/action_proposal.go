package httpfixture

import (
	"net/http"
	"net/url"
	"regexp"
	"sync/atomic"
	"testing"
)

func ProposedAction(t *testing.T, b *Browser, action string) string {
	t.Helper()
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {action}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	matches := regexp.MustCompile(`data-action-proposal="([0-9]+)"`).FindAllStringSubmatch(page, -1)
	if len(matches) == 0 {
		t.Fatal("concrete action card missing")
	}
	return "/bots/1/proposals/" + matches[len(matches)-1][1] + "/confirm"
}

func ActionProvider(action string) http.HandlerFunc {
	var calls atomic.Int64
	return func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			BuilderToolReply(w, "propose_action", map[string]string{"action": action})
		} else {
			BuilderTextReply(w)
		}
	}
}
