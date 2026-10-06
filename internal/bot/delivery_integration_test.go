package bot_test

import (
	"net/url"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestOwnerDeliveryMutationsRequireAuthorizationAndCSRF(t *testing.T) {
	a, b, _ := fixture.DeliveryFixture(t)
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("other-delivery@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/publish", "/bots/1/activate"} {
		if got := other.Post(path, url.Values{"operate": {"yes"}}); got.Code != 404 {
			t.Fatalf("other owner: %s %d", path, got.Code)
		}
		if got := b.Send("POST", path, url.Values{"operate": {"yes"}}); got.Code != 403 {
			t.Fatalf("missing CSRF: %s %d", path, got.Code)
		}
		if got := b.Send("GET", path, nil); path == "/bots/1/publish" && got.Code != 405 {
			t.Fatalf("GET publication: %d", got.Code)
		}
	}
}

func TestActivationInspectionLeavesPersistedObservationUnchanged(t *testing.T) {
	_, b, f := fixture.DeliveryFixture(t)
	f.Mu.Lock()
	f.Webhook = "https://foreign.example.test/secret"
	f.Mu.Unlock()
	if got := b.Send("GET", "/bots/1/activate", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "وب\u200cهوک سرویس دیگری") {
		t.Fatal("fresh activation inspection missing")
	}
	if got := b.Send("GET", "/bots/1", nil); strings.Contains(got.Body.String(), "از قبل تنظیم شده") {
		t.Fatal("GET changed the saved observation")
	}
}

func TestActivationRechecksConflictAndRetriesWithoutDroppingUpdates(t *testing.T) {
	_, b, f := fixture.DeliveryFixture(t)
	f.Mu.Lock()
	f.Webhook = "https://foreign.example.test/hidden-secret"
	f.Mu.Unlock()
	got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}})
	if got.Code != 409 || strings.Contains(got.Body.String(), "hidden-secret") {
		t.Fatalf("fresh conflict: %d", got.Code)
	}
	confirmation := fixture.HiddenValue(t, got.Body.String(), "conflict")
	f.Mu.Lock()
	f.ActivationFails = true
	f.Mu.Unlock()
	got = b.Post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {confirmation}})
	if got.Code != 503 {
		t.Fatalf("failure: %d", got.Code)
	}
	if page := b.Send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "فعال\u200cسازی ناموفق") {
		t.Fatal("failure state not persisted")
	}
	f.Mu.Lock()
	f.ActivationFails = false
	f.Mu.Unlock()
	if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}, "conflict": {confirmation}}); got.Code != 303 {
		t.Fatalf("retry: %d", got.Code)
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Webhook != "https://piko.example.test/telegram/bots/1" || f.Secret == "" || f.Secret == fixture.TestBotToken {
		t.Fatal("webhook endpoint or separate authentication missing")
	}
}
