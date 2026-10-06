package bot_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBotCardsOfferActionsForSavedFlowAndConnection(t *testing.T) {
	_, b, _ := fixture.GeneralDeployFixture(t, nil)
	if got := b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != http.StatusSeeOther {
		t.Fatalf("connect: %d", got.Code)
	}
	check := func(buildAction, connectionAction string) {
		t.Helper()
		for _, path := range []string{"/bots", "/dashboard"} {
			response := b.Send(http.MethodGet, path, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("%s: %d", path, response.Code)
			}
			_, card, found := strings.Cut(response.Body.String(), `<article class="piko-card piko-bot-card"`)
			if !found {
				t.Fatalf("%s missing Bot card", path)
			}
			card, _, _ = strings.Cut(card, "</article>")
			for _, want := range []string{`class="piko-bot-card-link" href="/bots/1"`, `href="/bots/1/studio"`, buildAction, "تلگرام", "جریان ربات"} {
				if !strings.Contains(card, want) {
					t.Errorf("%s missing card action %q", path, want)
				}
			}
			otherAction := "ساخت ربات"
			if buildAction == otherAction {
				otherAction = "ویرایش ربات"
			}
			if strings.Contains(card, otherAction) {
				t.Errorf("%s offers incorrect build action %q", path, otherAction)
			}
			for _, action := range []string{"connect", "connection"} {
				present := strings.Contains(card, `href="/bots/1/`+action+`"`)
				if present != (action == connectionAction) {
					t.Errorf("%s incorrect connection action %s", path, action)
				}
			}
		}
	}

	// A verified Telegram identity does not imply a saved implementation.
	check("ساخت ربات", "")
	if got := b.Send(http.MethodGet, "/bots/1/studio", nil); got.Code != http.StatusOK {
		t.Fatal("Build destination unavailable")
	}
	check("ساخت ربات", "")
	if got := b.PostDraft(t, "/bots/1/draft", fixture.WelcomeDraft()); got.Code != http.StatusSeeOther {
		t.Fatalf("save Draft: %d", got.Code)
	}
	// Saving a Draft is sufficient for Edit; publication is not required.
	check("ویرایش ربات", "")
	if got := b.Post("/bots/1/disconnect", url.Values{}); got.Code != http.StatusSeeOther {
		t.Fatalf("disconnect: %d", got.Code)
	}
	check("ویرایش ربات", "connection")
	if got := b.Send(http.MethodGet, "/bots/1/connection", nil); got.Code != http.StatusOK {
		t.Fatal("Reconnect destination unavailable")
	}
}

func TestUnconnectedBotCardOffersEditAndConnect(t *testing.T) {
	_, b := fixture.UnconnectedFixture(t)
	if got := b.Post("/bots/new", url.Values{"name": {"کارت <ربات>"}}); got.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", got.Code)
	}
	body := b.Send(http.MethodGet, "/bots", nil).Body.String()
	for _, want := range []string{"کارت &lt;ربات&gt;", "ویرایش ربات", `href="/bots/1/connect"`, `href="/bots/1/studio"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Unconnected Bot card missing %q", want)
		}
	}
	if got := b.Send(http.MethodGet, "/bots/1/connect", nil); got.Code != http.StatusOK {
		t.Fatal("Connect destination unavailable")
	}
}
