package app

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/bot/telegram"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestSubmissionWorkspacePaginationAndFrozenAnswers(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	name := `مینا <script>alert("answer")</script> Latin`
	for i := 1; i <= 51; i++ {
		d.text("/start", 2)
		d.press("درخواست", 1)
		if i == 51 {
			d.text(name, 1)
		} else {
			d.text(fmt.Sprintf("شرکت\u200cکننده %d", i), 1)
		}
		d.text("09123456789", 1)
		d.press("رد کردن", 4)
		d.press("ارسال", 1)
	}
	if got := d.b.Send("GET", "/bots/1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), `href="/bots/1/submissions"`) {
		t.Fatal("Bot overview does not lead to the inbox")
	}
	page := d.b.Send("GET", "/bots/1/submissions", nil)
	if page.Code != 200 || strings.Count(page.Body.String(), "data-submission-id=") != 50 {
		t.Fatal("first inbox page must contain 50 real Submissions")
	}
	for _, want := range []string{`href="/bots/1/submissions?before=2"`, `data-submission-id="51"`, `data-submission-id="2"`, `href="/bots/1/submissions/51"`, "aria-current=\"page\">درخواست\u200cهای دریافت\u200cشده", html.EscapeString(name)} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("inbox missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), `data-submission-id="1"`) || strings.Contains(page.Body.String(), name) {
		t.Fatal("inbox leaked cursor boundary or unescaped answer")
	}
	older := d.b.Send("GET", "/bots/1/submissions?before=2", nil)
	if older.Code != 200 || strings.Count(older.Body.String(), "data-submission-id=") != 1 || !strings.Contains(older.Body.String(), `data-submission-id="1"`) || !strings.Contains(older.Body.String(), "بازگشت به جدیدترین درخواست\u200cها") || strings.Contains(older.Body.String(), "درخواست\u200cهای قدیمی\u200cتر") {
		t.Fatal("older page lost boundary, end state, or return navigation")
	}
	if got := d.b.Send("GET", "/bots/1/submissions?before=1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "درخواست قدیمی\u200cتری برای نمایش وجود ندارد.") || !strings.Contains(got.Body.String(), "بازگشت به جدیدترین درخواست\u200cها") {
		t.Fatal("empty cursor page must explain its state and offer a return")
	}
	for _, cursor := range []string{"0", "-1", "bad", "9223372036854775808"} {
		if got := d.b.Send("GET", "/bots/1/submissions?before="+cursor, nil); got.Code != 400 {
			t.Fatalf("invalid cursor %q: %d", cursor, got.Code)
		}
	}
	// A later publication cannot change the original question labels or answers.
	draft := fixture.InquiryDraft()
	draft["question_label"][0] = "نام تازه"
	if got := d.b.PostDraft(t, "/bots/1/draft", draft); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	detail := d.b.Send("GET", "/bots/1/submissions/51", nil)
	for _, want := range []string{`<bdi dir="auto">` + html.EscapeString(name) + `</bdi>`, `<bdi dir="ltr">77</bdi>`, `نسخه رفتار</dt><dd>۲</dd>`, `href="/bots/1/submissions"`, "بدون پاسخ", "09123456789"} {
		if detail.Code != 200 || !strings.Contains(detail.Body.String(), want) {
			t.Fatalf("frozen detail missing %q: status %d", want, detail.Code)
		}
	}
	if strings.Contains(detail.Body.String(), "نام تازه") || strings.Contains(detail.Body.String(), name) {
		t.Fatal("later Draft changed frozen values or answer escaped its isolation")
	}
}

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
	neighbor := d.b.Send("GET", "/bots/1/submissions/2", nil).Body.String()
	for _, path := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		page := d.b.Send("GET", path, nil)
		if page.Code != 200 || !strings.Contains(page.Body.String(), `href="/bots/1/submissions/1/delete"`) {
			t.Fatalf("deletion control missing on %s", path)
		}
	}
	confirmationPage := d.b.Send("GET", "/bots/1/submissions/1/delete", nil)
	for _, want := range []string{`action="/bots/1/submissions/1/delete"`, "درخواست حذف", "حذف دائمی این درخواست", `href="/bots/1/submissions/1"`, "پیش\u200cنویس", "تعامل"} {
		if confirmationPage.Code != 200 || !strings.Contains(confirmationPage.Body.String(), want) {
			t.Fatalf("dedicated confirmation missing %q: status %d", want, confirmationPage.Code)
		}
	}
	d.countSubmissions(2) // Opening the confirmation must never delete anything.
	got := d.b.Post("/bots/1/submissions/1/delete", url.Values{})
	if got.Code != 303 || got.Header().Get("Location") != "/bots/1/submissions" {
		t.Fatalf("delete: status %d location %q", got.Code, got.Header().Get("Location"))
	}
	d.countSubmissions(1)
	if got := d.b.Send("GET", "/bots/1/submissions/1", nil); got.Code != 404 {
		t.Fatal("deleted detail remains accessible")
	}
	if got := d.b.Post("/bots/1/submissions/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal("repeated deletion must report missing record")
	}
	if got := d.b.Send("GET", "/bots/1/submissions/2", nil); got.Code != 200 || got.Body.String() != neighbor {
		t.Fatal("deletion changed neighboring record")
	}
	if got := webhook(d.a, d.Secret, callbackPayload(confirmationUpdate, "retry", 77, confirmation)); got.Code != 200 {
		t.Fatal("exact update retry rejected")
	}
	d.obsolete(confirmation)
	d.countSubmissions(1)
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.Router = restarted, restarted.server.Handler
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
	if got := d.b.Send("GET", "/bots/1/submissions/3", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "درخواست تازه") {
		t.Fatal("new independent attempt failed")
	}
	for _, id := range []string{"2", "3"} {
		if got := d.b.Post("/bots/1/submissions/"+id+"/delete", url.Values{}); got.Code != 303 {
			t.Fatal(got.Code)
		}
	}
	d.countSubmissions(0)
	if got := d.b.Send("GET", "/bots/1/submissions", nil); !strings.Contains(got.Body.String(), "درخواستی برای نمایش وجود ندارد.") {
		t.Fatal("empty inbox message missing after removal")
	}
	if got := d.b.Send("GET", "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "نام شما چیست؟") {
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
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	other.Send("GET", "/login", nil)
	if got := other.Send("GET", path, nil); got.Code != 303 || !strings.HasPrefix(got.Header().Get("Location"), "/login") {
		t.Fatal("anonymous confirmation exposed")
	}
	if got := other.Post(path, url.Values{}); got.Code != 303 || !strings.HasPrefix(got.Header().Get("Location"), "/login") {
		t.Fatal("anonymous deletion allowed")
	}
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("delete-other@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, p := range []string{"/bots/1/submissions", "/bots/1/submissions/1", "/bots/1/submissions/1/delete"} {
		if got := other.Send("GET", p, nil); got.Code != 404 || strings.Contains(got.Body.String(), "پاسخ خصوصی") {
			t.Fatal("cross-owner record exposed")
		}
	}
	if got := other.Post(path, url.Values{}); got.Code != 404 {
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
	d.b.Router = a.server.Handler
	if got := d.b.Post("/bots/connect", url.Values{"token": {fixture.TestBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Post("/bots/2/submissions/1/delete", url.Values{}); got.Code != 404 {
		t.Fatal("Submission deleted through another Bot URL")
	}
	if got := d.b.Send("GET", "/bots/2/submissions/1/delete", nil); got.Code != 404 {
		t.Fatal("Submission confirmation exposed through another Bot URL")
	}
	d.b.Router = d.a.server.Handler
	for _, tc := range []struct {
		name, method, path string
		form               url.Values
		status             int
	}{
		{"missing CSRF", "POST", path, url.Values{}, 403},
		{"invalid CSRF", "POST", path, url.Values{"csrf_token": {"invalid"}}, 403},
		{"confirmation GET", "GET", path, nil, 200},
		{"DELETE", "DELETE", path, url.Values{}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := d.b.Send(tc.method, tc.path, tc.form); got.Code != tc.status {
				t.Fatalf("status %d, want %d", got.Code, tc.status)
			}
		})
	}
	for _, id := range []string{"0", "-1", "bad", "9223372036854775808", "99"} {
		if got := d.b.Send("GET", "/bots/1/submissions/"+id+"/delete", nil); got.Code != 404 {
			t.Fatalf("invalid or missing confirmation %s: %d", id, got.Code)
		}
		if got := d.b.Post("/bots/1/submissions/"+id+"/delete", url.Values{}); got.Code != 404 {
			t.Fatalf("invalid or missing record %s: %d", id, got.Code)
		}
	}
	// Fault injection verifies the public error response and retained record.
	if _, err := d.a.db.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON bot_submissions BEGIN SELECT RAISE(ABORT,'private storage error'); END`); err != nil {
		t.Fatal(err)
	}
	if got := d.b.Post(path, url.Values{}); got.Code != 500 || strings.Contains(got.Body.String(), "private storage error") {
		t.Fatal("storage failure not handled safely")
	}
	d.countSubmissions(1)
	if got := d.b.Send("GET", "/bots/1/submissions/1", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "پاسخ خصوصی") {
		t.Fatal("rejected deletion changed record")
	}
}

func TestSubmissionDeletionPreservesOngoingInteractionAndWorkspace(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("درخواست پیشین", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("تعامل جاری", 1)
	retained := map[string]string{}
	for _, path := range []string{"/bots/1/settings", "/bots/1/connection", "/bots/1/draft"} {
		retained[path] = d.b.Send("GET", path, nil).Body.String()
	}
	if got := d.b.Post("/bots/1/submissions/1/delete", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for path, before := range retained {
		if got := d.b.Send("GET", path, nil); got.Code != 200 || got.Body.String() != before {
			t.Fatalf("Submission deletion changed %s", path)
		}
	}
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	if got := d.b.Send("GET", "/bots/1/submissions/2", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "تعامل جاری") {
		t.Fatal("deletion discarded the ongoing Interaction")
	}
	// Corrupt stored answers simulate a read failure without exposing storage details.
	if _, err := d.a.db.Exec(`UPDATE bot_submissions SET answers = 'private corrupt answers' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/bots/1/submissions", "/bots/1/submissions/2", "/bots/1/submissions/2/delete"} {
		if got := d.b.Send("GET", path, nil); got.Code != 500 || strings.Contains(got.Body.String(), "private corrupt answers") {
			t.Fatalf("unsafe read-error state on %s: %d", path, got.Code)
		}
	}
}
