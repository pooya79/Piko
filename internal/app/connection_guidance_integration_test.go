package app

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestCredentialFailuresKeepFocusedFormAndAllowRecovery(t *testing.T) {
	d := newInquiryDriver(t)
	d.f.Mu.Lock()
	d.f.IdentityID = 987654
	d.f.Mu.Unlock()
	got := d.b.Post("/bots/1/replace-token", url.Values{"token": {fixture.ReplacementToken}})
	if got.Code != 422 {
		t.Fatal(got.Code)
	}
	for _, want := range []string{`action="/bots/1/replace-token"`, `aria-invalid="true"`, `aria-describedby="lifecycle-token-hint lifecycle-token-error"`, `href="/bots/1/studio"`, "این توکن متعلق به ربات دیگری است"} {
		if !strings.Contains(got.Body.String(), want) {
			t.Errorf("credential recovery missing %q", want)
		}
	}
	if strings.Contains(got.Body.String(), fixture.ReplacementToken) {
		t.Fatal("credential error echoed secret")
	}
	d.f.Mu.Lock()
	d.f.IdentityID = 0
	d.f.Mu.Unlock()
	if got := d.b.Post("/bots/1/replace-token", url.Values{"token": {fixture.ReplacementToken}}); got.Code != 303 {
		t.Fatal("corrected token cannot be retried")
	}
}

func TestDisconnectHasDedicatedConfirmationAndRetainsStudio(t *testing.T) {
	d := newInquiryDriver(t)
	fixture.SeedBotChat(t, d.a.db, 1, "گفتگوی محفوظ")
	connection := d.b.Send("GET", "/bots/1/connection", nil)
	if !strings.Contains(connection.Body.String(), `href="/bots/1/connection/disconnect"`) || strings.Contains(connection.Body.String(), `action="/bots/1/disconnect"`) {
		t.Fatal("disconnection must have a separate focused screen")
	}
	page := d.b.Send("GET", "/bots/1/connection/disconnect", nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), `action="/bots/1/disconnect"`) || strings.Contains(page.Body.String(), `name="token"`) || !strings.Contains(page.Body.String(), "123456") || !strings.Contains(page.Body.String(), "گفتگوها") {
		t.Fatal("disconnection confirmation lacks identity or retention scope")
	}
	if got := d.b.Send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), `data-bot-state="active"`) {
		t.Fatal("opening confirmation changed operation")
	}
	before := fixture.RenderedDraft(t, d.b.Send("GET", "/bots/1/draft", nil).Body.String())
	if got := d.b.Post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if after := fixture.RenderedDraft(t, d.b.Send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(before, after) {
		t.Fatal("disconnection changed the Draft")
	}
	if page := d.b.Send("GET", "/bots/1/chats/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "گفتگوی محفوظ") {
		t.Fatal("disconnection lost saved chat")
	}
	if page := d.b.Send("GET", "/bots/1/connection", nil); !strings.Contains(page.Body.String(), `action="/bots/1/reconnect"`) || !strings.Contains(page.Body.String(), "123456") {
		t.Fatal("disconnection lost retained identity or reconnection form")
	}
}

func TestActivationFailureKeepsPublishedFlowAndRecoveryLinks(t *testing.T) {
	d := newInquiryDriver(t)
	d.b.Post("/bots/1/replace-token", url.Values{"token": {fixture.ReplacementToken}})
	d.f.Mu.Lock()
	d.f.APIStatus = 503
	d.f.Mu.Unlock()
	got := d.b.Post("/bots/1/activate", url.Values{"operate": {"yes"}})
	if got.Code != 503 {
		t.Fatal(got.Code)
	}
	for _, want := range []string{`href="/bots/1/activate"`, `href="/bots/1/connection"`, `href="/bots/1/studio"`, "نسخهٔ منتشرشده"} {
		if !strings.Contains(got.Body.String(), want) {
			t.Errorf("activation recovery missing %q", want)
		}
	}
	if strings.Contains(got.Body.String(), fixture.ReplacementToken) {
		t.Fatal("activation failure echoed credentials")
	}
	d.f.Mu.Lock()
	d.f.APIStatus = 0
	d.f.Mu.Unlock()
	if got := d.b.Send("GET", "/bots/1/activate", nil); got.Code != 200 {
		t.Fatal("failed activation cannot be inspected again")
	}
	if got := d.b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatal("failed activation cannot be retried")
	}
}

func TestDisconnectStorageFailureKeepsConfirmationAndAllowsRetry(t *testing.T) {
	d := newInquiryDriver(t)
	if _, err := d.a.db.Exec(`CREATE TRIGGER reject_disconnect BEFORE UPDATE OF encrypted_token ON bots BEGIN SELECT RAISE(ABORT,'private storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	got := d.b.Post("/bots/1/disconnect", url.Values{})
	if got.Code != 500 || !strings.Contains(got.Body.String(), `action="/bots/1/disconnect"`) || !strings.Contains(got.Body.String(), `href="/bots/1/connection"`) || strings.Contains(got.Body.String(), "private storage failure") {
		t.Fatal("disconnection failure lost focused confirmation or exposed diagnostics")
	}
	if _, err := d.a.db.Exec(`DROP TRIGGER reject_disconnect`); err != nil {
		t.Fatal(err)
	}
	if got := d.b.Post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal("disconnection failure blocked retry")
	}
}

func TestCredentialScreenRecoveryForVerificationFailures(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		for _, tc := range []struct {
			name            string
			APIStatus, want int
		}{
			{"revoked", 401, 422}, {"unavailable", 503, 503}, {"rejected", 400, 503}, {"forbidden", 403, 503},
		} {
			t.Run(fmt.Sprintf("reconnect=%t/%s", reconnect, tc.name), func(t *testing.T) {
				d := newInquiryDriver(t)
				action := "/bots/1/replace-token"
				if reconnect {
					d.b.Post("/bots/1/disconnect", url.Values{})
					action = "/bots/1/reconnect"
				}
				d.f.Mu.Lock()
				d.f.APIStatus = tc.APIStatus
				d.f.Mu.Unlock()
				got := d.b.Post(action, url.Values{"token": {fixture.ReplacementToken}})
				if got.Code != tc.want || !strings.Contains(got.Body.String(), `action="`+action+`"`) || strings.Contains(got.Body.String(), fixture.ReplacementToken) {
					t.Fatalf("credential recovery: %d", got.Code)
				}
				d.f.Mu.Lock()
				d.f.APIStatus = 0
				d.f.Mu.Unlock()
				if got := d.b.Post(action, url.Values{"token": {fixture.ReplacementToken}}); got.Code != 303 {
					t.Fatal("verification failure blocked corrected retry")
				}
			})
		}
	}
}

func TestDisconnectConfirmationRequiresOwnerAndDoesNotMutateOnGET(t *testing.T) {
	d := newInquiryDriver(t)
	guest := fixture.NewAccountBrowser(t, d.a.server.Handler)
	if got := guest.Send("GET", "/bots/1/connection/disconnect", nil); got.Code != 303 {
		t.Fatal("anonymous disconnection confirmation allowed")
	}
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("confirmation-other@example.test", "دیگر", "OwnerPassword123"))
	if got := other.Send("GET", "/bots/1/connection/disconnect", nil); got.Code != 404 || strings.Contains(got.Body.String(), "123456") {
		t.Fatal("cross-owner confirmation exposed identity")
	}
	if got := d.b.Send("GET", "/bots/1/connection/disconnect", nil); got.Code != 200 {
		t.Fatal(got.Code)
	}
	if got := d.b.Send("GET", "/bots/1", nil); !strings.Contains(got.Body.String(), `data-bot-state="active"`) {
		t.Fatal("confirmation GET changed Bot")
	}
}
