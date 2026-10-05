package app

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
)

func TestOwnerOrganizesSeparateBuilderChatsOnSharedDraft(t *testing.T) {
	_, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"ربات گفت\u200cوگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	for _, title := range []string{"پرسش\u200cهای مشتری", "منوی تازه <script>alert(1)</script>"} {
		if got := b.post("/bots/1/chats", url.Values{"title": {title}}); got.Code != 303 {
			t.Fatalf("create chat: %d", got.Code)
		}
	}
	list := b.send("GET", "/bots/1/chats", nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), `href="/bots/1/chats/1"`) || !strings.Contains(list.Body.String(), `href="/bots/1/chats/2"`) {
		t.Fatal("separate saved chats missing")
	}
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/2"} {
		page := b.send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `href="/bots/1/draft"`) || !strings.Contains(page.Body.String(), `disabled`) || strings.Contains(page.Body.String(), `action="`+path+`/messages"`) {
			t.Fatal("chat must retain manual settings and honest unavailable generation")
		}
	}
	if !strings.Contains(list.Body.String(), "&lt;script&gt;alert(1)&lt;/script&gt;") || strings.Contains(list.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("chat title was not escaped")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("chat creation changed the shared Draft")
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/chats/1", "/bots/1/chats/2"} {
		if got := b.send("GET", path, nil); got.Code != 200 || !strings.Contains(got.Body.String(), `data-draft-revision="2"`) {
			t.Fatal("chat did not refer to latest shared Draft")
		}
	}
}

func TestBuilderHistoryIsOrderedPrivateAndDurable(t *testing.T) {
	a, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"ربات تاریخچه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"تاریخچه اول", "تاریخچه دوم"} {
		if got := b.post("/bots/1/chats", url.Values{"title": {title}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
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
		if _, err := a.db.Exec("INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(?,?,?,?,1)", item.chat, item.seq, item.role, item.content); err != nil {
			t.Fatal(err)
		}
	}
	assertHistory := func(browser *accountBrowser) {
		t.Helper()
		got := browser.send("GET", "/bots/1/chats/1", nil)
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
	device := newAccountBrowser(t, a.server.Handler)
	device.send("GET", "/login", nil)
	if got := device.post("/login", url.Values{"email": {"bot-owner@example.test"}, "password": {"OwnerPassword123"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	assertHistory(device)
	b.post("/logout", url.Values{})
	if got := b.send("GET", "/bots/1/chats/1", nil); got.Code != 303 {
		t.Fatal("logged-out history was accessible")
	}
	b.send("GET", "/login", nil)
	if got := b.post("/login", url.Values{"email": {"bot-owner@example.test"}, "password": {"OwnerPassword123"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	assertHistory(b)
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient("http://127.0.0.1:1", http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	assertHistory(b)
}

func TestBuilderOwnershipMethodsValidationAndIdleDeletion(t *testing.T) {
	a, b := unconnectedFixture(t)
	for range 2 {
		if got := b.post("/bots/new", url.Values{"name": {"ربات"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	for _, v := range []url.Values{{}, {"title": {""}}, {"title": {"  "}}, {"title": {strings.Repeat("آ", 81)}}, {"title": {"one", "two"}}} {
		if got := b.post("/bots/1/chats", v); got.Code != 422 {
			t.Fatalf("title validation: %d", got.Code)
		}
	}
	for _, path := range []string{"/bots/1/chats", "/bots/2/chats"} {
		if got := b.post(path, url.Values{"title": {"گفت\u200cوگوی امن"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	for _, path := range []string{"/bots/1/chats/2", "/bots/2/chats/1", "/bots/1/chats/nope", "/bots/1/chats/0"} {
		if got := b.send("GET", path, nil); got.Code != 404 {
			t.Fatalf("wrong Bot/chat accepted: %s %d", path, got.Code)
		}
	}
	stranger := newAccountBrowser(t, a.server.Handler)
	stranger.send("GET", "/register", nil)
	if got := stranger.post("/register", registerValues("stranger@example.test", "دیگری", "StrangerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/chats", "/bots/1/chats/1"} {
		if got := stranger.send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "گفت\u200cوگوی امن") {
			t.Fatal("history exposed to another owner")
		}
	}
	for _, path := range []string{"/bots/1/chats", "/bots/1/chats/1/delete"} {
		if got := stranger.post(path, url.Values{"title": {"بیگانه"}}); got.Code != 404 {
			t.Fatalf("cross-owner mutation: %d", got.Code)
		}
		if got := b.send("POST", path, url.Values{"title": {"بدون توکن"}}); got.Code != 403 {
			t.Fatalf("missing CSRF: %d", got.Code)
		}
		if got := b.send("PUT", path, url.Values{"csrf_token": {b.cookie(auth.CSRFCookie)}}); got.Code != 405 {
			t.Fatalf("non-POST mutation: %d", got.Code)
		}
	}
	if got := b.send("GET", "/bots/1/chats/1/delete", nil); got.Code != 405 {
		t.Fatal("GET allowed deletion")
	}
	if _, err := a.db.Exec("INSERT INTO builder_messages VALUES(1,1,'result','تاریخچه حذف\u200cشدنی',1)"); err != nil {
		t.Fatal(err)
	}
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	// A failed cascaded deletion must leave both the chat and history available.
	if _, err := a.db.Exec("CREATE TRIGGER fail_chat_delete BEFORE DELETE ON builder_messages BEGIN SELECT RAISE(ABORT,'forced failure'); END"); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 500 {
		t.Fatal(got.Code)
	}
	if got := b.send("GET", "/bots/1/chats/1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "تاریخچه حذف\u200cشدنی") {
		t.Fatal("failed deletion lost history")
	}
	if _, err := a.db.Exec("DROP TRIGGER fail_chat_delete"); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 303 || got.Header().Get("Location") != "/bots/1/chats" {
		t.Fatal("idle deletion failed")
	}
	if got := b.send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("deleted history still accessible")
	}
	if got := b.send("GET", "/bots/2/chats/2", nil); got.Code != 200 {
		t.Fatal("deletion affected another chat")
	}
	if got := b.send("GET", "/bots/1", nil); got.Code != 200 {
		t.Fatal("deletion removed Bot")
	}
	if after := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("deletion changed Draft")
	}
	if got := b.post("/bots/1/chats", url.Values{"title": {"جدید"}}); got.Code != 303 || got.Header().Get("Location") == "/bots/1/chats/1" {
		t.Fatal("deleted chat URL reused")
	}
	if got := b.post("/bots/1/chats/3/delete", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/chats", url.Values{"title": {"جدیدتر"}}); got.Code != 303 || got.Header().Get("Location") == "/bots/1/chats/3" {
		t.Fatal("last deleted chat URL reused")
	}
	if got := b.post("/bots/2/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.send("GET", "/bots/2/chats/2", nil); got.Code != 404 {
		t.Fatal("Bot deletion retained accessible chat")
	}
}

func TestBuilderForwardMigrationAndExistingBotKeepPriorWork(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست پیشین", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پیشرفت پیشین", 1)
	preview := startLegacyPreview(t, d.a, d.b)
	before := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String())
	submission := d.b.send("GET", "/bots/1/submissions/1", nil).Body.String()
	previewBody := d.b.send("GET", preview, nil).Body.String()
	stop()
	legacy, err := database.Open(t.Context(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	rollbackToMigration(t, legacy, "000011_unconnected_bots")
	for range 2 {
		if err := database.Migrate(t.Context(), legacy, false); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	if got := d.b.post("/bots/1/chats", url.Values{"title": {"ربات موجود"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.send("GET", "/builder", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `href="/bots/1/chats"`) {
		t.Fatal("existing Bot missing from chat selection")
	}
	if got := d.b.send("GET", "/bots/1/chats/1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "ربات موجود") {
		t.Fatal("existing connected Bot cannot use chat")
	}
	if got := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(got, before) {
		t.Fatal("upgrade changed Draft or revision")
	}
	if got := d.b.send("GET", "/bots/1/submissions/1", nil); got.Code != 200 || got.Body.String() != submission {
		t.Fatal("upgrade changed Submission")
	}
	if got := d.b.send("GET", preview, nil); got.Code != 200 || got.Body.String() != previewBody {
		t.Fatal("upgrade changed Preview")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("upgrade lost credentials, published Flow or progress")
	}
	if got := d.b.post("/bots/1/chats/1/delete", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
}
