package app

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Opt-in browser fixture creates confirmed Submissions through fake Telegram
// and the real runtime, with a disposable migrated database.
func TestSubmissionBrowserFixture(t *testing.T) {
	addr := os.Getenv("PIKO_SUBMISSION_BROWSER_ADDR")
	if addr == "" {
		t.Skip("set PIKO_SUBMISSION_BROWSER_ADDR for browser verification")
	}
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	for i := 1; i <= 51; i++ {
		d.text("/start", 2)
		d.press("درخواست", 1)
		d.text(fmt.Sprintf("مینا Example %d", i), 1)
		d.text("09123456789", 1)
		if i == 51 {
			d.text("درخواست واقعی <script>alert('answer')</script>\n"+strings.Repeat("پاسخ بلند Mixed answer 123 — ", 30), 4)
		} else {
			d.press("رد کردن", 4)
		}
		d.press("ارسال", 1)
	}
	// The detail retains its original version after a new publication and attempt.
	draft := inquiryDraft()
	draft["question_label"][0] = "نام تازه"
	if got := d.b.postDraft(t, "/bots/1/draft", draft); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("تعامل ناتمام", 1)
	if got := d.b.post("/bots/new", url.Values{"name": {"ربات بدون درخواست"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("../../static"))))
	mux.Handle("/", d.a.server.Handler)
	server := &http.Server{Handler: mux}
	t.Cleanup(func() { _ = server.Close() })
	fmt.Printf("Submission browser fixture: http://%s\n", listener.Addr())
	if err := server.Serve(listener); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
