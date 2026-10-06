package app

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderShutdownPreventsStagedDraftCommit(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.BuilderFormDraft})
			return
		}
		close(started)
		<-r.Context().Done()
	})
	stop := startBuilderApp(t, a)
	before := b.LoadDraft(t, 1)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"تغییر پیش از توقف"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate not staged")
	}
	stop()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.Router = restarted.server.Handler
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	if strings.Contains(page, `data-after-revision=`) {
		t.Fatal("shutdown applied a candidate")
	}
	if after := b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("shutdown changed saved Draft")
	}
}

func TestBuilderDraftOutcomeMigrationPreservesExistingReplyAndBotData(t *testing.T) {
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
	draft := d.b.LoadDraft(t, 1)
	submission := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String()
	previewBody := d.b.Send("GET", preview, nil).Body.String()
	fixture.SeedBotChat(t, d.a.db, 1, "گفتگوی پیشین")
	stop()
	legacy, err := database.Open(context.Background(), d.a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	fixture.RollbackToMigration(t, legacy, "000014_builder_background")
	if _, err := legacy.Exec(`INSERT INTO builder_runs(owner_id,bot_id,chat_id,day,model,draft_revision,status,created_at,lease_until,finished_at) VALUES(1,1,1,'2026-10-04','legacy-model',1,'succeeded',1,0,2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'model','پاسخ قدیمی بدون تغییر پیش\u200cنویس',1)`); err != nil {
		t.Fatal(err)
	}
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
	page := d.b.Send("GET", "/bots/1/chats/1", nil).Body.String()
	if !strings.Contains(page, "پاسخ قدیمی بدون تغییر") || strings.Contains(page, `data-after-revision=`) || strings.Contains(page, `data-run-result="saved"`) {
		t.Fatal("upgrade fabricated Draft mutation or lost legacy reply")
	}
	if after := d.b.LoadDraft(t, 1); !reflect.DeepEqual(draft, after) {
		t.Fatal("upgrade changed Draft/account/session/Bot")
	}
	if page := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String(); page != submission {
		t.Fatal("upgrade lost Submission")
	}
	if page := d.b.Send("GET", preview, nil).Body.String(); page != previewBody {
		t.Fatal("upgrade lost Preview snapshot")
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("upgrade lost credentials, publications or progress")
	}
}
