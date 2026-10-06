package bot_test

import (
	"net/url"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestConnectionGuidesStudioDeploymentAndPausedReconnection(t *testing.T) {
	fake := &fixture.TelegramFake{}
	_, b, _ := fixture.BotFixture(t, fake.ServeHTTP)
	b.Post("/bots/new", url.Values{"name": {"کار محفوظ"}})
	b.Post("/bots/1/chats", url.Values{"title": {"گفتگوی محفوظ"}})
	studio := b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(studio, `href="/bots/1/connect"`) || strings.Contains(studio, `name="token"`) {
		t.Fatal("studio lacks a dedicated connection entry")
	}
	if got := b.Post("/bots/1/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := b.Send("GET", "/bots/1/connection", nil).Body.String()
	if !strings.Contains(page, "اتصال به\u200cتنهایی") || !strings.Contains(page, `href="/bots/1#bot-deployment"`) {
		t.Fatal("unpublished connection does not guide explicit deployment")
	}
	b.Post("/bots/1/publish", url.Values{})
	b.Post("/bots/1/activate", url.Values{"operate": {"yes"}})
	b.Post("/bots/1/pause", url.Values{})
	b.Post("/bots/1/disconnect", url.Values{})
	feedback := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}})
	if feedback.Code != 409 || !strings.Contains(feedback.Body.String(), `href="/bots/1/connection"`) {
		t.Fatal("deployment must guide a Disconnected Bot into reconnection")
	}
	if got := b.Post("/bots/1/reconnect", url.Values{"token": {fixture.ReplacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page = b.Send("GET", "/bots/1/connection", nil).Body.String()
	for _, want := range []string{`href="/bots/1/activate"`, "توقف ربات حفظ شده", "نسخهٔ منتشرشده"} {
		if !strings.Contains(page, want) {
			t.Errorf("paused reconnection missing %q", want)
		}
	}
	if strings.Contains(page, `action="/bots/1/resume"`) {
		t.Fatal("reconnection bypasses activation")
	}
	if got := b.Send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), `data-bot-state="published.inactive"`) {
		t.Fatal("reconnection activated or republished Bot")
	}
}
