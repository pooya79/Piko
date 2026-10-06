package bot_test

import (
	"net/url"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBotManagementNavigationUsesOwnedDestinations(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"مدیریت <ربات>"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	paths := []string{"/bots/1", "/bots/1/settings", "/bots/1/connection", "/bots/1/submissions", "/bots/1/draft", "/bots/1/studio"}
	visitor := fixture.NewAccountBrowser(t, a.Handler)
	for _, path := range append(paths, "/bots/1/studio") {
		if got := visitor.Send("GET", path, nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Errorf("anonymous management access at %s: %d", path, got.Code)
		}
	}
	for _, path := range paths {
		page := b.Send("GET", path, nil)
		if page.Code != 200 {
			t.Fatalf("management page %s: %d", path, page.Code)
		}
		body := page.Body.String()
		if !strings.Contains(body, `aria-label="مدیریت ربات"`) {
			t.Fatalf("missing management navigation at %s", path)
		}
		for _, link := range []string{"/bots/1", "/bots/1/studio", "/bots/1/settings", "/bots/1/connection", "/bots/1/submissions", "/bots/1/draft"} {
			if !strings.Contains(body, `href="`+link+`"`) {
				t.Errorf("missing %s at %s", link, path)
			}
		}
	}
	settings := b.Send("GET", "/bots/1/settings", nil).Body.String()
	for _, control := range []string{`action="/bots/1/name"`, `href="/bots/1/settings/delete"`} {
		if !strings.Contains(settings, control) {
			t.Errorf("settings missing %s", control)
		}
	}
	if strings.Contains(b.Send("GET", "/bots/1", nil).Body.String(), `name="token"`) {
		t.Fatal("overview requests credentials")
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("other-overview@example.test", "دیگر", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range append(paths, "/bots/1/studio") {
		if got := other.Send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "مدیریت &lt;ربات&gt;") {
			t.Errorf("management ownership at %s: %d", path, got.Code)
		}
	}
}

func TestBotOverviewTracksLifecycleAndRecoveryActions(t *testing.T) {
	_, b, _ := fixture.GeneralDeployFixture(t, nil)
	if got := b.Post("/bots/new", url.Values{"name": {"چرخهٔ ربات"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check := func(state string, present, absent []string) {
		t.Helper()
		got := b.Send("GET", "/bots/1", nil)
		body := got.Body.String()
		if got.Code != 200 || !strings.Contains(body, `data-bot-state="`+state+`"`) {
			t.Fatalf("overview does not identify %s: %d", state, got.Code)
		}
		for _, want := range present {
			if !strings.Contains(body, want) {
				t.Errorf("%s missing %s", state, want)
			}
		}
		for _, unwanted := range absent {
			if strings.Contains(body, unwanted) {
				t.Errorf("%s offers %s", state, unwanted)
			}
		}
		for _, path := range []string{"/dashboard", "/bots"} {
			if page := b.Send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), `data-bot-state="`+state+`"`) {
				t.Errorf("%s disagrees with overview state %s", path, state)
			}
		}
	}
	check("unconnected", []string{`href="/bots/1/connect"`}, []string{`id="deploy-submit"`, `action="/bots/1/pause"`, `action="/bots/1/resume"`, `data-persian-date`})
	if got := b.Post("/bots/1/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("inactive", []string{`id="deploy-submit"`, "فعال نشده"}, []string{`action="/bots/1/pause"`, `action="/bots/1/resume"`})
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("published.inactive", []string{"منتشر شده، اما فعال نیست", `href="/bots/1/activate"`}, []string{`action="/bots/1/pause"`})
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("active", []string{`action="/bots/1/pause"`}, []string{`action="/bots/1/resume"`})
	if got := b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("paused", []string{`action="/bots/1/resume"`}, []string{`action="/bots/1/pause"`})
	if got := b.Post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("disconnected", []string{`href="/bots/1/connection"`, "اتصال قطع شده"}, []string{`id="deploy-submit"`, `action="/bots/1/pause"`, `action="/bots/1/resume"`, `data-persian-date`})
	if got := b.Send("GET", "/bots/1/connection", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `action="/bots/1/reconnect"`) {
		t.Fatal("missing dedicated reconnection control")
	}
}

func TestBotStudioEntryOpensFreshConversationWithoutCreatingChats(t *testing.T) {
	a, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"استودیو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Send("GET", "/bots/1/studio", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `data-studio-unsaved`) || strings.Contains(got.Body.String(), `name="title"`) {
		t.Fatal("empty studio does not offer explicit chat creation")
	}
	if got := b.Send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("studio GET created a chat")
	}
	for _, title := range []string{"اول", "تازه"} {
		fixture.SeedBotChat(t, a.DB, 1, title)
	}
	if got := b.Send("GET", "/bots/1/studio", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `data-studio-unsaved`) || !strings.Contains(got.Body.String(), `href="/bots/1/chats/2"`) {
		t.Fatal("studio entry does not open a fresh conversation with saved chats accessible")
	}
	page := b.Send("GET", "/bots/1/chats/2", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `href="/bots/1/settings"`) || !strings.Contains(page.Body.String(), `data-piko-studio`) {
		t.Fatal("saved studio lacks management navigation")
	}
	if got := b.Send("GET", "/bots/1/chats/3", nil); got.Code != 404 {
		t.Fatal("studio entry created another chat")
	}
	for _, path := range []string{"/dashboard", "/bots"} {
		body := b.Send("GET", path, nil).Body.String()
		for _, link := range []string{"/bots/1", "/bots/new"} {
			if !strings.Contains(body, `href="`+link+`"`) {
				t.Errorf("%s missing working destination %s", path, link)
			}
		}
	}
}
