package app

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/builder"
)

func TestWorkspaceNameValidationKeepsSettingsAndEnteredName(t *testing.T) {
	_, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"نام اولیه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, name := range []string{"", "   ", strings.Repeat("س", 81), "نام\nنام"} {
		got := b.post("/bots/1/name", url.Values{"name": {name}})
		if got.Code != 422 || !strings.Contains(got.Body.String(), `id="bot-name-error"`) || !strings.Contains(got.Body.String(), `aria-invalid="true"`) || !strings.Contains(got.Body.String(), `action="/bots/1/name"`) {
			t.Fatalf("invalid name left settings or lost field feedback: %d", got.Code)
		}
		if current := b.send("GET", "/bots/1/settings", nil); !strings.Contains(current.Body.String(), `value="نام اولیه"`) {
			t.Fatal("invalid name changed workspace")
		}
	}
	name := strings.Repeat("ن", 80)
	got := b.post("/bots/1/name", url.Values{"name": {"  " + name + "  "}})
	if got.Code != 303 || got.Header().Get("Location") != "/bots/1/settings?saved=1" {
		t.Fatalf("valid name did not return to settings: %d %s", got.Code, got.Header().Get("Location"))
	}
	page := b.send("GET", got.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, `role="status"`) || !strings.Contains(page, `value="`+name+`"`) {
		t.Fatal("settings lost saved name or success feedback")
	}
	for _, names := range [][]string{nil, {"one", "two"}} {
		if got := b.post("/bots/1/name", url.Values{"name": names}); got.Code != 422 || !strings.Contains(got.Body.String(), `id="bot-name-error"`) {
			t.Fatal("malformed name lacks settings feedback")
		}
	}
}

func TestWorkspaceNameRequiresOwnerPOSTAndCSRF(t *testing.T) {
	a, b := unconnectedFixture(t)
	b.post("/bots/new", url.Values{"name": {"نام محفوظ"}})
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("name-other@example.test", "دیگر", "OwnerPassword123"))
	if got := other.post("/bots/1/name", url.Values{"name": {"تغییر نام"}}); got.Code != 404 {
		t.Fatal("cross-owner rename accepted")
	}
	guest := newAccountBrowser(t, a.server.Handler)
	guest.send("GET", "/login", nil)
	if got := guest.post("/bots/1/name", url.Values{"name": {"تغییر نام"}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous rename accepted")
	}
	for _, csrf := range []string{"", "invalid"} {
		if got := b.send("POST", "/bots/1/name", url.Values{"name": {"تغییر نام"}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal("rename accepted without CSRF")
		}
	}
	for _, method := range []string{"GET", "PUT", "PATCH"} {
		if got := b.send(method, "/bots/1/name", url.Values{"name": {"تغییر نام"}, "csrf_token": {b.cookie("piko_csrf")}}); got.Code != 405 {
			t.Fatalf("rename accepted %s: %d", method, got.Code)
		}
	}
	if page := b.send("GET", "/bots/1/settings", nil); !strings.Contains(page.Body.String(), `value="نام محفوظ"`) {
		t.Fatal("rejected rename changed Bot")
	}
}

func TestManualSettingsRemainSharedUnpublishedAndAvailableToFreshPreview(t *testing.T) {
	_, b := draftFixture(t)
	b.postDraft(t, "/bots/1/draft", combinedDraft())
	b.post("/bots/1/chats", url.Values{"title": {"تنظیمات مشترک"}})
	loaded := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	stale := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	loaded.Set("welcome", "سلام از تنظیمات دستی")
	if got := b.post("/bots/1/draft", loaded); got.Code != 303 {
		t.Fatal(got.Code)
	}
	stale.Set("welcome", "تغییر قدیمی")
	if got := b.post("/bots/1/draft", stale); got.Code != 409 || !strings.Contains(got.Body.String(), "تغییر قدیمی") {
		t.Fatal("stale manual configuration overwrote shared Draft")
	}
	current := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	loaded.Set("draft_revision", "2")
	if !reflect.DeepEqual(current, loaded) {
		t.Fatal("manual save lost retained Forms or menu order")
	}
	studio := b.send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(studio, "سلام از تنظیمات دستی") || !strings.Contains(studio, `data-flow-revision="2"`) {
		t.Fatal("studio does not inspect manual saved revision")
	}
	preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.send("GET", preview, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "سلام از تنظیمات دستی") {
		t.Fatal("fresh Preview missed manual save")
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), `data-bot-state="inactive"`) || strings.Contains(page.Body.String(), `data-bot-state="published.inactive"`) {
		t.Fatal("manual save published or activated Bot")
	}
}

func TestBotDeletionRemovesConvertedDiscussionAndAllAttachedChatsOnly(t *testing.T) {
	var calls atomic.Int64
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			memoryReply(w, "پاسخ پیش از ساخت")
		case 2:
			memoryReply(w, `{"intent":"build"}`)
		case 3:
			builderToolReply(w, "prepare_bot", map[string]string{"name": "ربات گفتگوی پیشین", "definition": builderFormDraft})
		default:
			memoryReply(w, "ربات آماده شد")
		}
	})
	converted := startPikoChat(t, b)
	b.post(converted+"/messages", url.Values{"message": {"گفتگوی عمومی پیش از ساخت"}})
	waitBuilder(t, b, converted, "succeeded")
	b.post(converted+"/messages", url.Values{"message": {"ربات بساز"}})
	page := waitBuilder(t, b, converted, "succeeded")
	if !strings.Contains(page, "گفتگوی عمومی پیش از ساخت") || !strings.Contains(page, `href="/bots/1/draft"`) {
		t.Fatal("fixture did not retain earlier discussion on conversion")
	}
	attached := b.post("/bots/1/chats", url.Values{"title": {"گفتگوی دوم"}}).Header().Get("Location")
	general := startPikoChat(t, b)
	b.post(general+"/messages", url.Values{"message": {"گفتگوی مستقل محفوظ"}})
	waitBuilder(t, b, general, "succeeded")
	b.post("/bots/new", url.Values{"name": {"ربات محفوظ"}})
	otherChat := b.post("/bots/2/chats", url.Values{"title": {"گفتگوی محفوظ"}}).Header().Get("Location")
	beforeOther := renderedDraft(t, b.send("GET", "/bots/2/draft", nil).Body.String())
	if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{converted, "/bots/1/chats/1", attached, "/bots/1/draft"} {
		if got := b.send("GET", path, nil); got.Code != 404 {
			t.Fatalf("deleted Bot retained discussion or Draft: %s", path)
		}
	}
	if got := b.send("GET", general, nil); got.Code != 200 || !strings.Contains(got.Body.String(), "گفتگوی مستقل محفوظ") || !strings.Contains(got.Body.String(), `data-admitted="3"`) {
		t.Fatal("Bot deletion changed unrelated general chat or accounting")
	}
	if got := b.send("GET", otherChat, nil); got.Code != 200 {
		t.Fatal("Bot deletion removed another Bot's chat")
	}
	if got := renderedDraft(t, b.send("GET", "/bots/2/draft", nil).Body.String()); !reflect.DeepEqual(got, beforeOther) {
		t.Fatal("Bot deletion changed another Draft")
	}
}

func TestBotDeletionHasFocusedOwnerConfirmationAndRecovery(t *testing.T) {
	a, b := unconnectedFixture(t)
	b.post("/bots/new", url.Values{"name": {"حذف <ربات>"}})
	path := "/bots/1/settings/delete"
	page := b.send("GET", path, nil)
	for _, want := range []string{`action="/bots/1/delete"`, "حذف &lt;ربات&gt;", "پیام\u200cهای عمومی پیش از ساخت ربات", "ربات\u200cهای دیگر", `href="/bots/1/settings"`, `name="confirm_delete"`} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("focused confirmation missing %s: %d", want, page.Code)
		}
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("settings-other@example.test", "دیگر", "OwnerPassword123"))
	if got := other.send("GET", path, nil); got.Code != 404 {
		t.Fatal("cross-owner deletion confirmation exposed Bot")
	}
	for _, values := range []url.Values{{}, {"confirm_delete": {"no"}}, {"confirm_delete": {"yes", "yes"}}} {
		if got := b.post("/bots/1/delete", values); got.Code != 422 || !strings.Contains(got.Body.String(), `id="delete-error"`) || !strings.Contains(got.Body.String(), `action="/bots/1/delete"`) {
			t.Fatal("invalid confirmation lost recovery controls")
		}
	}
	if got := b.send("GET", "/bots/1", nil); got.Code != 200 {
		t.Fatal("viewing or rejecting confirmation deleted Bot")
	}
}
