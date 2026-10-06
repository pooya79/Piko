package app

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"testing"

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

func TestSubmissionDeletionUnavailableAndRetainsRecords(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	for _, name := range []string{"درخواست نخست", "درخواست دوم"} {
		d.text("/start", 2)
		d.press("درخواست", 1)
		d.text(name, 1)
		d.text("09123456789", 1)
		d.press("رد کردن", 4)
		d.press("ارسال", 1)
	}
	for _, path := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		page := d.b.Send("GET", path, nil)
		if page.Code != 200 || strings.Contains(page.Body.String(), "/delete") || strings.Contains(page.Body.String(), "حذف درخواست") {
			t.Fatalf("submission page still offers deletion: %s", path)
		}
	}
	for _, method := range []string{"GET", "POST"} {
		var gotCode int
		if method == "POST" {
			gotCode = d.b.Post("/bots/1/submissions/1/delete", url.Values{}).Code
		} else {
			gotCode = d.b.Send(method, "/bots/1/submissions/1/delete", nil).Code
		}
		if gotCode != 404 {
			t.Fatalf("removed %s deletion route returned %d", method, gotCode)
		}
	}
	d.countSubmissions(2)
	for _, id := range []string{"1", "2"} {
		if got := d.b.Send("GET", "/bots/1/submissions/"+id, nil); got.Code != 200 {
			t.Fatal("record lost", got.Code)
		}
	}
}

func TestSubmissionsRequireOwnerAndMatchingBot(t *testing.T) {
	d := newInquiryDriver(t)
	runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("پاسخ خصوصی", 1)
	d.text("09123456789", 1)
	d.press("رد کردن", 4)
	d.press("ارسال", 1)
	other := fixture.NewAccountBrowser(t, d.a.server.Handler)
	if got := other.Send("GET", "/bots/1/submissions/1", nil); got.Code != 303 {
		t.Fatal("anonymous record exposed")
	}
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("submission-other@example.test", "Other", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, path := range []string{"/bots/1/submissions", "/bots/1/submissions/1"} {
		if got := other.Send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "پاسخ خصوصی") {
			t.Fatal("cross-owner record exposed")
		}
	}
	if got := d.b.Post("/bots/new", url.Values{"name": {"ربات دیگر"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.Send("GET", "/bots/2/submissions/1", nil); got.Code != 404 {
		t.Fatal("record exposed under another Bot")
	}
	for _, id := range []string{"0", "-1", "bad", "9223372036854775808", "99"} {
		if got := d.b.Send("GET", "/bots/1/submissions/"+id, nil); got.Code != 404 {
			t.Fatal("invalid record accepted", got.Code)
		}
	}
}

func TestSubmissionRejectedDeletionPreservesInteractionAndWorkspace(t *testing.T) {
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
	if got := d.b.Post("/bots/1/submissions/1/delete", url.Values{}); got.Code != 404 {
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
	d.countSubmissions(2)
	if got := d.b.Send("GET", "/bots/1/submissions/2", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "تعامل جاری") {
		t.Fatal("deletion discarded the ongoing Interaction")
	}
	// Corrupt stored answers simulate a read failure without exposing storage details.
	if _, err := d.a.db.Exec(`UPDATE bot_submissions SET answers = 'private corrupt answers' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/bots/1/submissions", "/bots/1/submissions/2"} {
		if got := d.b.Send("GET", path, nil); got.Code != 500 || strings.Contains(got.Body.String(), "private corrupt answers") {
			t.Fatalf("unsafe read-error state on %s: %d", path, got.Code)
		}
	}
}
