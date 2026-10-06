package builder_test

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestOwnerOrganizesSeparateBuilderChatsOnSharedDraft(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات گفت\u200cوگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	for _, title := range []string{"پرسش\u200cهای مشتری", "منوی تازه <script>alert(1)</script>"} {
		fixture.SeedBotChat(t, a.DB, 1, title)
	}
	list := b.Send("GET", "/bots/1/studio", nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `href="/bots/1/chats/1"`) || !strings.Contains(list.Body.String(), `href="/bots/1/chats/2"`) {
		t.Fatal("separate saved chats missing")
	}
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/2"} {
		page := b.Send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `href="/bots/1/draft"`) || !strings.Contains(page.Body.String(), `disabled`) || strings.Contains(page.Body.String(), `action="`+path+`/messages"`) {
			t.Fatal("chat must retain manual settings and honest unavailable generation")
		}
	}
	if !strings.Contains(list.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(list.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("chat title was not escaped")
	}
	if after := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("chat creation changed the shared Draft")
	}
	if got := b.Post("/bots/1/draft", fixture.DraftAtRevision(fixture.InquiryDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/2"} {
		if got := b.Send("GET", path, nil); got.Code != 200 || !strings.Contains(got.Body.String(), `data-draft-revision="2"`) {
			t.Fatal("chat did not refer to latest shared Draft")
		}
	}
}

func TestBuilderHistoryIsOrderedPrivateAndDurable(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات تاریخچه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"تاریخچه اول", "تاریخچه دوم"} {
		fixture.SeedBotChat(t, a.DB, 1, title)
	}
	// Historical records represent results saved by the later runtime slice.
	// Insert out of order and with tied timestamps to exercise durable ordering.
	for _, item := range []struct {
		chat, seq     int
		role, content string
	}{
		{1, 3, "result", "نتیجه سوم <img src=x onerror=alert(1)>"},
		{1, 1, "owner", "پیام اول"},
		{2, 1, "owner", "حافظه خصوصی دوم"},
		{1, 2, "model", "پاسخ دوم"},
	} {
		if _, err := a.DB.Exec("INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(?,?,?,?,1)", item.chat, item.seq, item.role, item.content); err != nil {
			t.Fatal(err)
		}
	}
	assertHistory := func(browser *fixture.Browser) {
		t.Helper()
		got := browser.Send("GET", "/bots/1/chats/1", nil)
		body := got.Body.String()
		first, second, third := strings.Index(body, "پیام اول"), strings.Index(body, "پاسخ دوم"), strings.Index(body, "نتیجه سوم")
		if got.Code != 200 || first < 0 || second <= first || third <= second || strings.Contains(body, "حافظه خصوصی دوم") {
			t.Fatal("history lost its order or leaked another chat")
		}
		if strings.Contains(body, "<img src=x") || !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") || !strings.Contains(body, `<bdi dir="auto">پیام اول</bdi>`) {
			t.Fatal("history must escape and direction-isolate owner/model/result text")
		}
	}
	assertHistory(b)
	assertHistory(b) // Refresh.
	device := fixture.NewAccountBrowser(t, a.Handler)
	device.Send("GET", "/login", nil)
	if got := device.Post("/login", url.Values{"email": {"bot-owner@example.test"}, "password": {"OwnerPassword123"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	assertHistory(device)
	b.Post("/logout", url.Values{})
	if got := b.Send("GET", "/bots/1/chats/1", nil); got.Code != 303 {
		t.Fatal("logged-out history was accessible")
	}
	b.Send("GET", "/login", nil)
	if got := b.Post("/login", url.Values{"email": {"bot-owner@example.test"}, "password": {"OwnerPassword123"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	assertHistory(b)
	if err := a.DB.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := fixture.NewWithTelegram(t.Context(), a.Config, telegram.NewClient("http://127.0.0.1:1", http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	assertHistory(b)
}

func TestBuilderOwnershipMethodsValidationAndUnavailableDeletion(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	for range 2 {
		if got := b.Post("/bots/new", url.Values{"name": {"ربات"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	for _, v := range []url.Values{{}, {"title": {""}}, {"title": {"  "}}, {"title": {strings.Repeat("آ", 81)}}, {"title": {"one", "two"}}} {
		if got := b.Post("/bots/1/chats", v); got.Code != 422 {
			t.Fatalf("title validation: %d", got.Code)
		}
	}
	for _, botID := range []int64{1, 2} {
		fixture.SeedBotChat(t, a.DB, botID, "گفت\u200cوگوی امن")
	}
	for _, path := range []string{"/bots/1/chats/2", "/bots/2/chats/1", "/bots/1/chats/nope", "/bots/1/chats/0"} {
		if got := b.Send("GET", path, nil); got.Code != 404 {
			t.Fatalf("wrong Bot/chat accepted: %s %d", path, got.Code)
		}
	}
	stranger := fixture.NewAccountBrowser(t, a.Handler)
	stranger.Send("GET", "/register", nil)
	if got := stranger.Post("/register", fixture.RegisterValues("stranger@example.test", "دیگری", "StrangerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/chats", "/bots/1/chats/1"} {
		if got := stranger.Send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "گفت\u200cوگوی امن") {
			t.Fatal("history exposed to another owner")
		}
	}
	for _, path := range []string{"/bots/1/chats"} {
		if got := stranger.Post(path, url.Values{"title": {"بیگانه"}}); got.Code != 404 {
			t.Fatalf("cross-owner mutation: %d", got.Code)
		}
		if got := b.Send("POST", path, url.Values{"title": {"بدون توکن"}}); got.Code != 403 {
			t.Fatalf("missing CSRF: %d", got.Code)
		}
		if got := b.Send("PUT", path, url.Values{"csrf_token": {b.Cookie(auth.CSRFCookie)}}); got.Code != 405 {
			t.Fatalf("non-POST mutation: %d", got.Code)
		}
	}
	for _, path := range []string{"/bots/1/chats/1/delete", "/chats/1/delete"} {
		if got := b.Send("GET", path, nil); got.Code != 404 {
			t.Fatal("removed confirmation route remains available", got.Code)
		}
		if got := b.Post(path, url.Values{}); got.Code != 404 {
			t.Fatal("removed deletion action remains available", got.Code)
		}
	}
	if got := b.Send("GET", "/bots/1/chats/1", nil); got.Code != 200 || strings.Contains(got.Body.String(), "studio-delete") {
		t.Fatal("chat was deleted or still offers deletion")
	}
	if got := b.Post("/bots/2/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Send("GET", "/bots/2/chats/2", nil); got.Code != 404 {
		t.Fatal("Bot deletion retained accessible chat")
	}
}
