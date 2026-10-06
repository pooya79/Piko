package bot_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestTokenCreationShowsGuideThenOwnedStudioAndDashboard(t *testing.T) {
	var calls atomic.Int64
	a, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
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
	form := b.Send("GET", "/bots/new", nil)
	if form.Code != 200 || form.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("creation form: %d", form.Code)
	}
	for _, want := range []string{`href="https://t.me/BotFather"`, "/newbot", "/mybots", `name="token"`, `type="password"`, `name="csrf_token"`, `action="/bots/new"`, "تأیید توکن و ساخت ربات"} {
		if !strings.Contains(form.Body.String(), want) {
			t.Errorf("setup missing %q", want)
		}
	}
	if calls.Load() != 0 || b.Send("GET", "/bots/1", nil).Code != 404 || b.Send("GET", "/chats/1", nil).Code != 404 {
		t.Fatal("opening setup created work or contacted Telegram")
	}
	created := b.Post("/bots/new", url.Values{"token": {fixture.TestBotToken}})
	if created.Code != 303 || created.Header().Get("Location") != "/bots/1/created" || calls.Load() != 2 {
		t.Fatalf("token creation: %d %s", created.Code, created.Header().Get("Location"))
	}
	for range 2 {
		page := b.Send("GET", "/bots/1/created", nil)
		if page.Code != 200 || page.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("success page: %d", page.Code)
		}
		for _, want := range []string{"ربات ساخته شد", "Created &lt;bot&gt;", "@created_bot", `href="/bots/1/studio"`, `href="/bots/1"`, "داشبورد ربات"} {
			if !strings.Contains(page.Body.String(), want) {
				t.Errorf("success missing %q", want)
			}
		}
		if strings.Contains(page.Body.String(), fixture.TestBotToken) {
			t.Fatal("success reflected token")
		}
	}
	if calls.Load() != 2 || b.Send("GET", "/bots/2", nil).Code != 404 {
		t.Fatal("refresh repeated creation or verification")
	}
	if b.Send("GET", "/bots/1/chats/1", nil).Code != 404 {
		t.Fatal("success page created a chat before the owner chose to start one")
	}
	studio := b.Send("GET", "/bots/1/studio", nil)
	if studio.Code != 200 || !strings.Contains(studio.Body.String(), `data-piko-studio`) || !strings.Contains(studio.Body.String(), `data-studio-unsaved`) || !strings.Contains(studio.Body.String(), `id="builder-message"`) {
		t.Fatal("chat button does not reach the unsaved bot conversation")
	}
	if b.Send("GET", "/bots/1/chats/1", nil).Code != 404 {
		t.Fatal("opening the bot studio saved an empty chat")
	}
	if dashboard := b.Send("GET", "/bots/1", nil); dashboard.Code != 200 || !strings.Contains(dashboard.Body.String(), `data-bot-state="inactive"`) {
		t.Fatal("dashboard button does not reach the inactive bot overview")
	}
	if duplicate := b.Post("/bots/new", url.Values{"token": {fixture.TestBotToken}}); duplicate.Code != 409 || strings.Contains(duplicate.Body.String(), fixture.TestBotToken) {
		t.Fatal("duplicate created or exposed credentials")
	}
	if b.Send("GET", "/bots/2", nil).Code != 404 {
		t.Fatal("duplicate created a second bot")
	}
	visitor := fixture.NewAccountBrowser(t, a.Handler)
	if got := visitor.Send("GET", "/bots/1/created", nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous success access")
	}
	visitor.Send("GET", "/register", nil)
	visitor.Post("/register", fixture.RegisterValues("creation-other@example.test", "Other", "OwnerPassword123"))
	if got := visitor.Send("GET", "/bots/1/created", nil); got.Code != 404 || strings.Contains(got.Body.String(), "created_bot") {
		t.Fatal("success exposed another owner's bot")
	}
}

func TestTokenCreationFailuresRetainSetupWithoutSavingBot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		Tokens []string
		status int
	}{
		{"missing", nil, 422},
		{"empty", []string{""}, 422},
		{"malformed", []string{"bad-token"}, 422},
		{"multiple", []string{fixture.TestBotToken, fixture.TestBotToken}, 422},
		{"rejected", []string{fixture.TestBotToken}, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, _ := fixture.BotFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.name != "rejected" {
					t.Error("invalid input reached Telegram")
				}
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, `{"ok":false}`)
			})
			b.Send("GET", "/bots/new", nil)
			got := b.Post("/bots/new", url.Values{"token": tc.Tokens})
			if got.Code != tc.status || !strings.Contains(got.Body.String(), `action="/bots/new"`) || !strings.Contains(got.Body.String(), "BotFather") || !strings.Contains(got.Body.String(), "توکن معتبر نیست") || strings.Contains(got.Body.String(), fixture.TestBotToken) {
				t.Fatalf("creation failure: %d", got.Code)
			}
			if b.Send("GET", "/bots/1", nil).Code != 404 || b.Send("GET", "/bots/1/created", nil).Code != 404 {
				t.Fatal("failed verification saved a bot")
			}
		})
	}
}

func TestTokenCreationRequiresAuthenticationAndCSRF(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	visitor := fixture.NewAccountBrowser(t, a.Handler)
	if got := visitor.Send("GET", "/bots/new", nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous setup access")
	}
	visitor.Send("GET", "/register", nil)
	if got := visitor.Post("/bots/new", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
		t.Fatal("anonymous token creation")
	}
	b.Send("GET", "/bots/new", nil)
	for _, csrf := range []string{"", "wrong"} {
		if got := b.Send("POST", "/bots/new", url.Values{"token": {fixture.TestBotToken}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal("token creation bypassed CSRF")
		}
	}
	if got := b.Send("GET", "/bots/new?token="+fixture.TestBotToken, nil); got.Code != 200 || strings.Contains(got.Body.String(), fixture.TestBotToken) {
		t.Fatal("GET consumed or reflected token")
	}
	if got := b.Send("PUT", "/bots/new", url.Values{"token": {fixture.TestBotToken}, "csrf_token": {b.Cookie("piko_csrf")}}); got.Code != 405 {
		t.Fatal("non-POST creation admitted")
	}
	if b.Send("GET", "/bots/1", nil).Code != 404 {
		t.Fatal("rejected creation saved a bot")
	}
}
