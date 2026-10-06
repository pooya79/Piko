package app

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestDraftRevisionUpgradeRetainsOwnerAndBotData(t *testing.T) {
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
	before := d.b.LoadDraft(t, 1)
	priorRevision := before.Get("draft_revision")
	before.Set("draft_revision", "1") // Existing pre-revision Drafts begin at one.
	submission := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String()
	previewBody := d.b.Send("GET", preview, nil).Body.String()
	// The pre-revision upgrade also changes the current Draft metadata shown
	// alongside the retained legacy Preview, without changing its snapshot.
	previewBody = strings.Replace(previewBody, `data-preview-draft-revision="`+priorRevision+`"`, `data-preview-draft-revision="1"`, 1)
	stop()
	legacy, err := database.Open(t.Context(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = legacy.Close() }()
	// Drop only the new column in this disposable file to represent version 9.
	fixture.RollbackToMigration(t, legacy, "000009_bot_pause")
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
	if got := d.b.LoadDraft(t, 1); !reflect.DeepEqual(got, before) || got.Get("draft_revision") != "1" {
		t.Fatal("upgrade changed the saved Draft, owner session or initial revision")
	}
	if got := d.b.Send("GET", "/bots/1/submissions/1", nil); got.Code != 200 || got.Body.String() != submission {
		t.Fatal("upgrade changed collected Submission")
	}
	if got := d.b.Send("GET", preview, nil); got.Code != 200 || got.Body.String() != previewBody {
		t.Fatal("upgrade changed Preview snapshot")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("upgrade lost published version, credentials or unfinished answers")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if page := d.b.Send("GET", "/bots/1/submissions/2", nil); !strings.Contains(page.Body.String(), "پیشرفت پیشین") {
		t.Fatal("upgrade lost unfinished answers")
	}
	if err := d.b.SaveDraft(t, 1, fixture.DraftAtRevision(fixture.WelcomeDraft(), "0")); err == nil {
		t.Fatal("rejected Draft change was accepted")
	}
	if err := d.b.SaveDraft(t, 1, before); err != nil {
		t.Fatal(err)
	}
	if got := d.b.LoadDraft(t, 1); got.Get("draft_revision") != "2" {
		t.Fatal("migrated Draft did not advance")
	}
}
