package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/pooya79/Piko/internal/bot/flow"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

// The opt-in browser fixture serves disposable migrated data and fake Telegram.
func TestFlowBrowserFixture(t *testing.T) {
	addr := os.Getenv("PIKO_FLOW_BROWSER_ADDR")
	if addr == "" {
		t.Skip("set PIKO_FLOW_BROWSER_ADDR for browser verification")
	}
	fake := &fixture.TelegramFake{}
	a, b, _ := botFixture(t, fake.ServeHTTP)
	b.Post("/bots/new", url.Values{"name": {"ربات آزمایش جریان"}})
	if err := b.SaveDraft(t, 1, fixture.WelcomeDraft()); err != nil {
		t.Fatal(err)
	}
	b.Post("/bots/1/publish", url.Values{})
	if err := b.SaveDraft(t, 1, fixture.CombinedDraft()); err != nil {
		t.Fatal(err)
	}
	fixture.SeedBotChat(t, a.db, 1, "گفتگوی اول")
	fixture.SeedBotChat(t, a.db, 1, "گفتگوی دوم")
	b.Post("/bots/new", url.Values{"name": {"ربات خالی"}})
	b.Post("/bots/new", url.Values{"name": {"جریان بزرگ"}})
	d := flow.Definition{Version: 2, Welcome: flow.Block{ID: "welcome", Type: "message", Text: "خوش آمدید"}, Menu: flow.Block{ID: "menu", Type: "menu", Text: "یک مسیر انتخاب کنید"}}
	for i := 1; i <= flow.MaxChoices; i++ {
		id := fmt.Sprintf("form%d", i)
		f := flow.Form{ID: id, Review: "بررسی پاسخ\u200cها", Acknowledgement: "پاسخ\u200cها دریافت شد"}
		for j := 1; j <= flow.MaxQuestions; j++ {
			f.Questions = append(f.Questions, flow.Question{ID: fmt.Sprintf("q%d", j), Type: "short_text", Label: fmt.Sprintf("پرسش %d", j), Prompt: fmt.Sprintf("پرسش %d در فرم %d", j, i), Required: j%2 == 0, MaxLength: 200})
		}
		d.Forms = append(d.Forms, f)
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: id, Target: id, Label: fmt.Sprintf("فرم %d", i)})
	}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SaveDraft(t, 3, url.Values{"definition": {string(data)}}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../static"))))
	mux.Handle("/", a.server.Handler)
	server := &http.Server{Handler: mux}
	t.Cleanup(func() { _ = server.Close() })
	fmt.Printf("Flow browser fixture: http://%s\n", listener.Addr())
	if err := server.Serve(listener); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
