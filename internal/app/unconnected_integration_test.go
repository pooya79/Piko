package app

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func unconnectedFixture(t *testing.T) (*App, *fixture.Browser) {
	t.Helper()
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Unconnected Bot work contacted Telegram")
		w.WriteHeader(500)
	})
	return a, b
}

func TestUnconnectedUpgradePreservesPopulatedBotsAndLifecycleStates(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست محفوظ", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پیشرفت محفوظ", 1)
	preview := startLegacyPreview(t, d.a, d.b)
	d.f.Mu.Lock()
	d.f.IdentityID = 222222
	d.f.Mu.Unlock()
	if got := d.b.Post("/bots/connect", url.Values{"token": {fixture.ReplacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/2/disconnect", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := map[string]string{}
	for _, path := range []string{"/account", "/bots/1/submissions/1", preview, "/bots/2"} {
		before[path] = d.b.Send("GET", path, nil).Body.String()
	}
	stop()
	legacy, err := database.Open(t.Context(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	fixture.RollbackToMigration(t, legacy, "000010_draft_revisions")
	for range 2 {
		if err := database.Migrate(t.Context(), legacy, false); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.Router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	for path, body := range before {
		if page := d.b.Send("GET", path, nil); page.Code != 200 || page.Body.String() != body {
			t.Fatalf("upgrade altered retained data: %s", path)
		}
	}
	if got := d.b.Post("/bots/new", url.Values{"name": {"ربات بدون اتصال"}}); got.Code != 303 || got.Header().Get("Location") != "/bots/3/studio" {
		t.Fatal("upgraded installation cannot create an Unconnected Bot")
	}
	for _, path := range []string{"/bots", "/dashboard"} {
		page := d.b.Send("GET", path, nil).Body.String()
		for _, status := range []string{"هنوز متصل نشده", "اتصال قطع شده", "تأیید شده"} {
			if !strings.Contains(page, status) {
				t.Fatalf("distinct lifecycle state %q missing at %s", status, path)
			}
		}
	}
	if got := d.b.Post("/bots/connect", url.Values{"token": {fixture.ReplacementToken}}); got.Code != 409 {
		t.Fatal("upgrade released a disconnected identity")
	}
	d.f.Mu.Lock()
	d.f.IdentityID = 0
	d.f.Mu.Unlock()
	if got := d.b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 409 {
		t.Fatal("upgrade released a connected identity")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if page := d.b.Send("GET", "/bots/1/submissions/2", nil); !strings.Contains(page.Body.String(), "پیشرفت محفوظ") {
		t.Fatal("upgrade lost unfinished answers or published Flow")
	}
	if got := d.b.Post("/bots/2/reconnect", url.Values{"token": {fixture.ReplacementToken}}); got.Code != 422 {
		t.Fatal("disconnected Bot allowed a different identity")
	}
	d.f.Mu.Lock()
	d.f.IdentityID = 222222
	d.f.Mu.Unlock()
	if got := d.b.Post("/bots/2/reconnect", url.Values{"token": {fixture.ReplacementToken}}); got.Code != 303 {
		t.Fatal("retained identity cannot reconnect")
	}
}
