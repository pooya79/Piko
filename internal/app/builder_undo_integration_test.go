package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestBuilderUndoLeavesLivePublicationInteractionsAndSubmissionsIntact(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("نام ثبت\u200cشده", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("تعامل فعال", 1)
	before := d.b.LoadDraft(t, 1)
	submission := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String()
	var calls atomic.Int64
	fake := httptest.NewServer(fixture.StreamingBuilderProvider(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1)%2 == 1 {
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": fixture.StructuredDraft})
		} else {
			fixture.BuilderTextReply(w)
		}
	}))
	t.Cleanup(fake.Close)
	if err := d.a.builder.Configure(builder.Config{APIKey: "test-server-key", BaseURL: fake.URL + "/v1"}, time.Now); err != nil {
		t.Fatal(err)
	}
	fixture.SeedBotChat(t, d.a.db, 1, "ویرایش پیش\u200cنویس")
	fixture.SaveBuilderChange(t, d.b, "/bots/1/chats/1")
	if got := d.b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	botPage := d.b.Send("GET", "/bots/1", nil).Body.String()
	if got := d.b.Post("/bots/1/chats/1/runs/1/undo", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before.Set("draft_revision", "4")
	if after := d.b.LoadDraft(t, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("Undo failed to restore Form configuration")
	}
	if page := d.b.Send("GET", "/bots/1", nil).Body.String(); page != botPage {
		t.Fatal("Undo changed publication/delivery/pause")
	}
	if page := d.b.Send("GET", "/bots/1/submissions/1", nil).Body.String(); page != submission {
		t.Fatal("Undo changed a completed Submission")
	}
	if got := d.b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 1)
	d.press("ادامه", 1)
	sent := waitSent(t, d.f, d.Sent)
	if sent[len(sent)-1].Text != "شماره تماس شما چیست؟" {
		t.Fatal("Undo changed a version-pinned Interaction")
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	d.text("/start", 2)
	sent = waitSent(t, d.f, d.Sent)
	if sent[len(sent)-2].Text != "Hello" {
		t.Fatal("Undo reverted the newly published Flow for fresh Interactions")
	}
}
