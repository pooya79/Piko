package httpfixture

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/builder"
)

func UndoFixture(t *testing.T) (*HTTP, *Browser) {
	t.Helper()
	var calls atomic.Int64
	return BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			BuilderToolReply(w, "prepare_draft", map[string]string{"definition": StructuredDraft})
		} else {
			BuilderTextReply(w)
		}
	})
}

func SaveBuilderChange(t *testing.T, b *Browser, chat string) string {
	t.Helper()
	if got := b.Post(chat+"/messages", url.Values{"message": {"منوی ساعت کار بساز"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return WaitBuilder(t, b, chat, "succeeded")
}
