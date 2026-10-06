package app

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

// Opt-in browser fixture uses the same App HTTP seam and disposable migrated
// SQLite as integration tests. No model or real Telegram credentials are used.
func TestConnectionBrowserFixture(t *testing.T) {
	addr := os.Getenv("PIKO_CONNECTION_BROWSER_ADDR")
	settings := os.Getenv("PIKO_SETTINGS_BROWSER_ADDR")
	if settings != "" {
		addr = settings
	}
	if addr == "" {
		t.Skip("set PIKO_CONNECTION_BROWSER_ADDR for manual browser verification")
	}
	fake := &fixture.TelegramFake{}
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/bot999999:") && strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":999999,"is_bot":true,"first_name":"Other Bot","username":"other_bot"}}`)
			return
		}
		fake.ServeHTTP(w, r)
	})
	if got := b.Post("/bots/new", url.Values{"name": {"ربات آزمایش اتصال"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.Post("/bots/1/chats", url.Values{"title": {"گفتگوی محفوظ"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if settings != "" {
		if got := b.PostDraft(t, "/bots/1/draft", fixture.CombinedDraft()); got.Code != 303 {
			t.Fatal(got.Code)
		}
		b.Post("/bots/1/chats", url.Values{"title": {"گفتگوی دوم"}})
		b.Post("/chats", url.Values{})
		b.Post("/bots/new", url.Values{"name": {"ربات مستقل"}})
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
	fmt.Printf("Connection browser fixture: http://%s\n", listener.Addr())
	if err := server.Serve(listener); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
