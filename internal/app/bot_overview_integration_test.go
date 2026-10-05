package app

import (
	"net/url"
	"strings"
	"testing"
)

func TestBotManagementNavigationUsesOwnedDestinations(t *testing.T) {
	a, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"مدیریت <ربات>"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	paths := []string{"/bots/1", "/bots/1/settings", "/bots/1/connection", "/bots/1/submissions", "/bots/1/draft", "/bots/1/chats"}
	visitor := newAccountBrowser(t, a.server.Handler)
	for _, path := range append(paths, "/bots/1/studio") {
		if got := visitor.send("GET", path, nil); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Errorf("anonymous management access at %s: %d", path, got.Code)
		}
	}
	for _, path := range paths {
		page := b.send("GET", path, nil)
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
	settings := b.send("GET", "/bots/1/settings", nil).Body.String()
	for _, control := range []string{`action="/bots/1/name"`, `href="/bots/1/settings/delete"`} {
		if !strings.Contains(settings, control) {
			t.Errorf("settings missing %s", control)
		}
	}
	if strings.Contains(b.send("GET", "/bots/1", nil).Body.String(), `name="token"`) {
		t.Fatal("overview requests credentials")
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("other-overview@example.test", "دیگر", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range append(paths, "/bots/1/studio") {
		if got := other.send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "مدیریت &lt;ربات&gt;") {
			t.Errorf("management ownership at %s: %d", path, got.Code)
		}
	}
}

func TestBotOverviewTracksLifecycleAndRecoveryActions(t *testing.T) {
	_, b, _ := generalDeployFixture(t, nil)
	if got := b.post("/bots/new", url.Values{"name": {"چرخهٔ ربات"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check := func(state string, present, absent []string) {
		t.Helper()
		got := b.send("GET", "/bots/1", nil)
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
			if page := b.send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), `data-bot-state="`+state+`"`) {
				t.Errorf("%s disagrees with overview state %s", path, state)
			}
		}
	}
	check("unconnected", []string{`href="/bots/1/connect"`}, []string{`id="deploy-submit"`, `action="/bots/1/pause"`, `action="/bots/1/resume"`, `data-persian-date`})
	if got := b.post("/bots/1/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("inactive", []string{`id="deploy-submit"`, "فعال نشده"}, []string{`action="/bots/1/pause"`, `action="/bots/1/resume"`})
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("published.inactive", []string{"منتشر شده، اما فعال نیست", `href="/bots/1/activate"`}, []string{`action="/bots/1/pause"`})
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("active", []string{`action="/bots/1/pause"`}, []string{`action="/bots/1/resume"`})
	if got := b.post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("paused", []string{`action="/bots/1/resume"`}, []string{`action="/bots/1/pause"`})
	if got := b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	check("disconnected", []string{`href="/bots/1/connection"`, "اتصال قطع شده"}, []string{`id="deploy-submit"`, `action="/bots/1/pause"`, `action="/bots/1/resume"`, `data-persian-date`})
	if got := b.send("GET", "/bots/1/connection", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `action="/bots/1/reconnect"`) {
		t.Fatal("missing dedicated reconnection control")
	}
}

func TestBotStudioEntryOpensSavedWorkWithoutCreatingChats(t *testing.T) {
	_, b := unconnectedFixture(t)
	if got := b.post("/bots/new", url.Values{"name": {"استودیو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.send("GET", "/bots/1/studio", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `action="/bots/1/chats"`) {
		t.Fatal("empty studio does not offer explicit chat creation")
	}
	if got := b.send("GET", "/bots/1/chats/1", nil); got.Code != 404 {
		t.Fatal("studio GET created a chat")
	}
	for _, title := range []string{"اول", "تازه"} {
		if got := b.post("/bots/1/chats", url.Values{"title": {title}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	if got := b.send("GET", "/bots/1/studio", nil); got.Code != 303 || got.Header().Get("Location") != "/bots/1/chats/2" {
		t.Fatal("studio entry does not open the latest saved chat")
	}
	page := b.send("GET", "/bots/1/chats/2", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `href="/bots/1/settings"`) || !strings.Contains(page.Body.String(), `data-piko-studio`) {
		t.Fatal("saved studio lacks management navigation")
	}
	if got := b.send("GET", "/bots/1/chats/3", nil); got.Code != 404 {
		t.Fatal("studio entry created another chat")
	}
	for _, path := range []string{"/dashboard", "/bots"} {
		body := b.send("GET", path, nil).Body.String()
		for _, link := range []string{"/bots/1", "/bots/new"} {
			if !strings.Contains(body, `href="`+link+`"`) {
				t.Errorf("%s missing working destination %s", path, link)
			}
		}
	}
}
