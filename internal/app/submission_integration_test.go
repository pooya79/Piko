package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
)

func TestSubmissionDeletionRetainsNeighborsAndRejectsOldConfirmations(t *testing.T) {
	d := newInquiryDriver(t)
	stop := runDeliveryApp(t, d.a)
	var confirmation string
	var confirmationUpdate int
	for _, name := range []string{"درخواست حذف", "درخواست باقی"} {
		d.text("/start", 2)
		d.press("درخواست", 1)
		d.text(name, 1)
		d.text("09123456789", 1)
		d.press("رد کردن", 4)
		if confirmation == "" {
			confirmation = d.button("ارسال")
			confirmationUpdate = d.update + 1
		}
		d.press("ارسال", 1)
	}
	d.countSubmissions(2)
	neighbor := d.b.send("GET", "/bots/1/submissions/2", nil).Body.String()
	for _, path := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		page := d.b.send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `action="/bots/1/submissions/1/delete"`) {
			t.Fatalf("deletion control missing on %s", path)
		}
	}
	got := d.b.post("/bots/1/submissions/1/delete", url.Values{})
	if got.Code != 303 || got.Header().Get("Location") != "/bots/1/submissions" {
		t.Fatalf("delete: status %d location %q", got.Code, got.Header().Get("Location"))
	}
	d.countSubmissions(1)
	if got := d.b.send("GET", "/bots/1/submissions/1", nil); got.Code != 404 {
		t.Fatal("deleted detail remains accessible")
	}
	if got := d.b.post("/bots/1/submissions/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal("repeated deletion must report missing record")
	}
	if got := d.b.send("GET", "/bots/1/submissions/2", nil); got.Code != 200 || got.Body.String() != neighbor {
		t.Fatal("deletion changed neighboring record")
	}
	if got := webhook(d.a, d.secret, callbackPayload(confirmationUpdate, "retry", 77, confirmation)); got.Code != 200 {
		t.Fatal("exact update retry rejected")
	}
	d.obsolete(confirmation)
	d.countSubmissions(1)
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	d.obsolete(confirmation)
	d.countSubmissions(1)
	// A fresh attempt still operates on the retained Bot, credentials and publication.
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست تازه", 1)
	d.obsolete(confirmation)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.obsolete(confirmation)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if got := d.b.send("GET", "/bots/1/submissions/3", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "درخواست تازه") {
		t.Fatal("new independent attempt failed")
	}
	for _, id := range []string{"2", "3"} {
		if got := d.b.post("/bots/1/submissions/"+id+"/delete", url.Values{}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	d.countSubmissions(0)
	if got := d.b.send("GET", "/bots/1/submissions", nil); !strings.Contains(got.Body.String(), "درخواستی برای نمایش وجود ندارد.") {
		t.Fatal("empty inbox message missing after removal")
	}
	if got := d.b.send("GET", "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "نام شما چیست؟") {
		t.Fatal("deletion changed Bot configuration")
	}
}

func TestSubmissionDeletionRequiresOwnerCSRFAndMatchingBot(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پاسخ خصوصی", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	path := "/bots/1/submissions/1/delete"
	other := newAccountBrowser(t, d.a.server.Handler)
	other.send("GET", "/login", nil)
	if got := other.post(path, url.Values{}); got.Code != 303 || !strings.HasPrefix(got.Header().Get("Location"), "/login") {
		t.Fatal("anonymous deletion allowed")
	}
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("delete-other@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, p := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.send("GET", p, nil); got.Code != 404 || strings.Contains(got.Body.String(), "پاسخ خصوصی") {
			t.Fatal("cross-owner record exposed")
		}
	}
	if got := other.post(path, url.Values{}); got.Code != 404 {
		t.Fatal("cross-owner deletion allowed")
	}
	// Connect a second owned Bot through fake Telegram, then try the first
	// Submission ID under that Bot's URL. Neither owner access nor a valid ID
	// alone may authorize deletion of a record belonging to another Bot.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":654321,"is_bot":true,"first_name":"Other Bot","username":"other_bot"}}`)
			return
		}
		d.f.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	a, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(endpoint.URL, endpoint.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	d.b.router = a.server.Handler
	if got := d.b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/2/submissions/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal("Submission deleted through another Bot URL")
	}
	d.b.router = d.a.server.Handler
	for _, tc := range []struct {
		name, method, path string
		form               url.Values
		status             int
	}{
		{"missing CSRF", "POST", path, url.Values{}, 403},
		{"invalid CSRF", "POST", path, url.Values{"csrf_token": {"invalid"}}, 403},
		{"GET", "GET", path, nil, 405},
		{"DELETE", "DELETE", path, url.Values{}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := d.b.send(tc.method, tc.path, tc.form); got.Code != tc.status {
				t.Fatalf("status %d, want %d", got.Code, tc.status)
			}
		})
	}
	for _, id := range []string{"0", "-1", "bad", "9223372036854775808", "99"} {
		if got := d.b.post("/bots/1/submissions/"+id+"/delete", url.Values{}); got.Code != 404 {
			t.Fatalf("invalid or missing record %s: %d", id, got.Code)
		}
	}
	// Fault injection verifies the public error response and retained record.
	if _, err := d.a.db.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON bot_submissions BEGIN SELECT RAISE(ABORT,'private storage error'); END`); err != nil {
		t.Fatal(err)
	}
	if got := d.b.post(path, url.Values{}); got.Code != 500 || strings.Contains(got.Body.String(), "private storage error") {
		t.Fatal("storage failure not handled safely")
	}
	d.countSubmissions(1)
	if got := d.b.send("GET", "/bots/1/submissions/1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "پاسخ خصوصی") {
		t.Fatal("rejected deletion changed record")
	}
}
