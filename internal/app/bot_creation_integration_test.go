package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTokenCreationShowsGuideThenOwnedStudioAndDashboard(t *testing.T) {
	var calls atomic.Int64
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Created <bot>","username":"created_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getWebhookInfo"):
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		default:
			t.Error("creation changed Telegram delivery")
			w.WriteHeader(500)
		}
	})
	form := b.send("GET", "/bots/new", nil)
	if form.Code != 200 || form.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("creation form: %d", form.Code)
	}
	for _, want := range []string{`href="https://t.me/BotFather"`, "/newbot", "/mybots", `name="token"`, `type="password"`, `name="csrf_token"`, `action="/bots/new"`, "تأیید توکن و ساخت ربات"} {
		if !strings.Contains(form.Body.String(), want) {
			t.Errorf("setup missing %q", want)
		}
	}
	if calls.Load() != 0 || b.send("GET", "/bots/1", nil).Code != 404 || b.send("GET", "/chats/1", nil).Code != 404 {
		t.Fatal("opening setup created work or contacted Telegram")
	}
	created := b.post("/bots/new", url.Values{"token": {testBotToken}})
	if created.Code != 303 || created.Header().Get("Location") != "/bots/1/created" || calls.Load() != 2 {
		t.Fatalf("token creation: %d %s", created.Code, created.Header().Get("Location"))
	}
	for range 2 {
		page := b.send("GET", "/bots/1/created", nil)
		if page.Code != 200 || page.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("success page: %d", page.Code)
		}
		for _, want := range []string{"ربات ساخته شد", "Created &lt;bot&gt;", "@created_bot", `action="/bots/1/chats"`, `href="/bots/1"`, "داشبورد ربات"} {
			if !strings.Contains(page.Body.String(), want) {
				t.Errorf("success missing %q", want)
			}
		}
		if strings.Contains(page.Body.String(), testBotToken) {
			t.Fatal("success reflected token")
		}
	}
	if calls.Load() != 2 || b.send("GET", "/bots/2", nil).Code != 404 {
		t.Fatal("refresh repeated creation or verification")
	}
	if b.send("GET", "/bots/1/chats/1", nil).Code != 404 {
		t.Fatal("success page created a chat before the owner chose to start one")
	}
	chat := b.post("/bots/1/chats", url.Values{"title": {"شروع گفت\u200cوگوی ربات"}})
	if chat.Code != 303 || chat.Header().Get("Location") != "/bots/1/chats/1" {
		t.Fatal("chat button does not start the bot's builder chat")
	}
	studio := b.send("GET", chat.Header().Get("Location"), nil)
	if studio.Code != 200 || !strings.Contains(studio.Body.String(), `data-piko-studio`) || !strings.Contains(studio.Body.String(), `id="builder-message"`) {
		t.Fatal("chat button does not reach the bot conversation")
	}
	if dashboard := b.send("GET", "/bots/1", nil); dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), `data-bot-state="inactive"`) {
		t.Fatal("dashboard button does not reach the inactive bot overview")
	}
	if duplicate := b.post("/bots/new", url.Values{"token": {testBotToken}}); duplicate.Code != 409 || strings.Contains(duplicate.Body.String(), testBotToken) {
		t.Fatal("duplicate created or exposed credentials")
	}
	if b.send("GET", "/bots/2", nil).Code != 404 {
		t.Fatal("duplicate created a second bot")
	}
	visitor := newAccountBrowser(t, a.server.Handler)
	if got := visitor.send("GET", "/bots/1/created", nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous success access")
	}
	visitor.send("GET", "/register", nil)
	visitor.post("/register", registerValues("creation-other@example.test", "Other", "OwnerPassword123"))
	if got := visitor.send("GET", "/bots/1/created", nil); got.Code != 404 || strings.Contains(got.Body.String(), "created_bot") {
		t.Fatal("success exposed another owner's bot")
	}
}

func TestTokenCreationFailuresRetainSetupWithoutSavingBot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tokens []string
		status int
	}{
		{"missing", nil, 422},
		{"empty", []string{""}, 422},
		{"malformed", []string{"bad-token"}, 422},
		{"multiple", []string{testBotToken, testBotToken}, 422},
		{"rejected", []string{testBotToken}, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.name != "rejected" {
					t.Error("invalid input reached Telegram")
				}
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"ok":false}`)
			})
			b.send("GET", "/bots/new", nil)
			got := b.post("/bots/new", url.Values{"token": tc.tokens})
			if got.Code != tc.status || !strings.Contains(got.Body.String(), `action="/bots/new"`) || !strings.Contains(got.Body.String(), "BotFather") || !strings.Contains(got.Body.String(), "توکن معتبر نیست") || strings.Contains(got.Body.String(), testBotToken) {
				t.Fatalf("creation failure: %d", got.Code)
			}
			if b.send("GET", "/bots/1", nil).Code != 404 || b.send("GET", "/bots/1/created", nil).Code != 404 {
				t.Fatal("failed verification saved a bot")
			}
		})
	}
}

func TestTokenCreationRequiresAuthenticationAndCSRF(t *testing.T) {
	a, b := unconnectedFixture(t)
	visitor := newAccountBrowser(t, a.server.Handler)
	if got := visitor.send("GET", "/bots/new", nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous setup access")
	}
	visitor.send("GET", "/register", nil)
	if got := visitor.post("/bots/new", url.Values{"token": {testBotToken}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous token creation")
	}
	b.send("GET", "/bots/new", nil)
	for _, csrf := range []string{"", "wrong"} {
		if got := b.send("POST", "/bots/new", url.Values{"token": {testBotToken}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal("token creation bypassed CSRF")
		}
	}
	if got := b.send("GET", "/bots/new?token="+testBotToken, nil); got.Code != 200 || strings.Contains(got.Body.String(), testBotToken) {
		t.Fatal("GET consumed or reflected token")
	}
	if got := b.send("PUT", "/bots/new", url.Values{"token": {testBotToken}, "csrf_token": {b.cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatal("non-POST creation admitted")
	}
	if b.send("GET", "/bots/1", nil).Code != 404 {
		t.Fatal("rejected creation saved a bot")
	}
}
