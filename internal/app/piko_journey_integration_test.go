package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestPikoCreatedTemplatesDeployRetryPauseAndPinParticipantVersion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		draft  flow.Definition
		answer string
	}{
		{"inquiry", inquiry.Default().Definition(), "09123456789"},
		{"registration", registration.Default().Definition(), "دوره پیشرفته"},
		{"booking", booking.Default().Definition(), "1405/07/11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(tc.draft)
			changed := tc.draft
			// Copy the Questions so the expected original publication stays independent.
			changed.Forms = append([]flow.Form(nil), tc.draft.Forms...)
			changed.Forms[0].Questions = append([]flow.Question(nil), tc.draft.Forms[0].Questions...)
			changed.Forms[0].Questions[1].Prompt = "پرسش نسخه جدید؟"
			changed.Forms[0].Acknowledgement = "دریافت نسخه جدید"
			updated, _ := json.Marshal(changed)
			var calls atomic.Int64
			a, b, f := generalDeployFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ربات درخواست", "definition": string(data)})
				case 4:
					fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": string(updated)})
				default:
					fixture.MemoryReply(w, "طرح آماده شد")
				}
			})
			chat := fixture.StartPikoChat(t, b)
			if got := b.Post(chat+"/messages", url.Values{"message": {"ربات " + tc.name + " بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, chat, "succeeded")
			if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 409 || !strings.Contains(got.Body.String(), "/bots/1/connect") {
				t.Fatal("missing connection guidance", got.Code)
			}
			if got := b.Post("/bots/1/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			f.Mu.Lock()
			f.ActivationFails = true
			f.Mu.Unlock()
			if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 503 || !strings.Contains(got.Body.String(), `data-deploy-version="1"`) {
				t.Fatal("publication/activation outcomes conflated", got.Code)
			}
			f.Mu.Lock()
			f.ActivationFails = false
			f.Mu.Unlock()
			if got := b.Post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, "نسخهٔ منتشرشده: ۱") {
				t.Fatal("retry republished")
			}
			f.Mu.Lock()
			secret := f.Secret
			f.Mu.Unlock()
			d := &formDriver{t: t, a: a, b: b, f: f, Secret: secret, update: 100}
			runDeliveryApp(t, a)
			d.text("/start", 2)
			d.press(tc.draft.Menu.Choices[0].Label, 1)
			if got := b.Post(chat+"/messages", url.Values{"message": {"پرسش دوم و پیام دریافت را تغییر بده"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, chat, "succeeded")
			if got := b.Post("/bots/1/pause", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if got := b.Post("/bots/1/deploy", url.Values{"operate": {"yes"}}); got.Code != 200 || !strings.Contains(got.Body.String(), `data-deploy-version="2"`) {
				t.Fatal(got.Code)
			}
			if page := b.Send("GET", "/bots/1", nil).Body.String(); !strings.Contains(page, `action="/bots/1/resume"`) {
				t.Fatal("Deploy resumed paused Bot")
			}
			d.text("پاسخ در حالت توقف", 1)
			d.countSubmissions(0)
			if got := b.Post("/bots/1/resume", url.Values{}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			d.text("مینا", 1)
			sent := waitSent(t, f, d.Sent)
			if !strings.Contains(sent[len(sent)-1].Text, tc.draft.Forms[0].Questions[1].Prompt) {
				t.Fatal("unfinished Participant moved to new Flow version", sent[len(sent)-1].Text)
			}
			d.text(tc.answer, 1)
			last := "جزئیات آزمایشی"
			if tc.name == "registration" {
				last = "2"
			}
			d.text(last, 4)
			d.press("ارسال", 1)
			d.countSubmissions(1)
			sent = waitSent(t, f, d.Sent)
			if !strings.Contains(sent[len(sent)-1].Text, tc.draft.Forms[0].Acknowledgement) {
				t.Fatal("confirmation lost original publication", sent[len(sent)-1].Text)
			}
			d.press("شروع دوباره", 2)
			d.press(tc.draft.Menu.Choices[0].Label, 1)
			d.text("سارا", 1)
			sent = waitSent(t, f, d.Sent)
			if !strings.Contains(sent[len(sent)-1].Text, "پرسش نسخه جدید؟") {
				t.Fatal("new Interaction did not use latest publication")
			}
			if calls.Load() != 5 {
				t.Fatal("lifecycle invoked model")
			}
		})
	}
}
