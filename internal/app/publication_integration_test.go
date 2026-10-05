package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestOwnerPublishesSavedDraftWithoutActivatingDelivery(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 422 {
		t.Fatalf("missing Draft: %d", got.Code)
	}
	if got := b.postDraft(t, "/bots/1/draft", url.Values{"definition": {structuredDraft}}); got.Code != 303 {
		t.Fatalf("save: %d", got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatalf("publish: %d", got.Code)
	}
	got := b.send(http.MethodGet, "/bots/1", nil)
	if !strings.Contains(got.Body.String(), "نسخهٔ منتشرشده: ۱") || !strings.Contains(got.Body.String(), `data-bot-state="published.inactive"`) {
		t.Fatal("publication or delivery state missing")
	}
}

func TestPublicationValidatesStoredDraftAndKeepsExistingVersion(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.postDraft(t, "/bots/1/draft", url.Values{"definition": {structuredDraft}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if _, err := a.db.Exec(`UPDATE bot_drafts SET definition='{"version":999}' WHERE bot_id=1`); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 422 {
		t.Fatalf("invalid publication: %d", got.Code)
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "نسخهٔ منتشرشده: ۱") {
		t.Fatal("invalid Draft changed publication")
	}
}
