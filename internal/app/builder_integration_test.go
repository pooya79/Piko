package app

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

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
	before := fixture.RenderedDraft(t, d.b.Send("GET", "/bots/1/draft", nil).Body.String())
	submission := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String()
	previewBody := d.b.Send("GET", preview, nil).Body.String()
	stop()
	legacy, err := database.Open(t.Context(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	fixture.RollbackToMigration(t, legacy, "000011_unconnected_bots")
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
	fixture.SeedBotChat(t, d.a.db, 1, "ربات موجود")
	if got := d.b.Send("GET", "/builder", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `href="/bots/1/studio"`) {
		t.Fatal("existing Bot missing from chat selection")
	}
	if got := d.b.Send("GET", "/bots/1/chats/1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "ربات موجود") {
		t.Fatal("existing connected Bot cannot use chat")
	}
	if got := fixture.RenderedDraft(t, d.b.Send("GET", "/bots/1/draft", nil).Body.String()); !reflect.DeepEqual(got, before) {
		t.Fatal("upgrade changed Draft or revision")
	}
	if got := d.b.Send("GET", "/bots/1/submissions/1", nil); got.Code != 200 || got.Body.String() != submission {
		t.Fatal("upgrade changed Submission")
	}
	if got := d.b.Send("GET", preview, nil); got.Code != 200 || got.Body.String() != previewBody {
		t.Fatal("upgrade changed Preview")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("upgrade lost credentials, published Flow or progress")
	}
	if got := d.b.Post("/bots/1/chats/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal(got.Code)
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
}
