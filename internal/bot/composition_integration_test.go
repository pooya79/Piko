package bot_test

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestCombinedFormsMenuRouting(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if err := b.SaveDraft(t, 1, fixture.CombinedDraft()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ choice, prompt string }{{"ask", "نام؟"}, {"apply", "دوره؟"}, {"book", "تاریخ؟"}} {
		path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
		if got := b.Post(path+"/choose", url.Values{"choice": {tc.choice}, "revision": {"1"}}); got.Code != 303 {
			t.Fatalf("route %s: %d", tc.choice, got.Code)
		}
		if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), tc.prompt) {
			t.Fatalf("route %s missing %s", tc.choice, tc.prompt)
		}
	}
}

func TestCombinedFormsAllValidatorsRunInIsolatedPreview(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	v := fixture.CombinedDraft()
	v["text_max"][0] = "3"
	v["text_max"][2] = "5"
	v["date_min"][5] = "1405/1/1"
	v["date_max"][5] = "1405/12/29"
	if err := b.SaveDraft(t, 1, v); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		choice string
		steps  []struct{ key, value, want string }
	}{
		{"ask", []struct{ key, value, want string }{{"answer", " ", "الزامی"}, {"answer", "چهار", "بیش از اندازه"}, {"answer", "مینا", "بیش از اندازه"}, {"answer", "سار", "تماس؟"}, {"answer", "abc", "شماره تلفن معتبر"}, {"answer", "۰۹۱۲۳۴۵۶۷۸۹", "درخواست؟"}, {"answer", "بسیار بلند", "بیش از اندازه"}, {"choice", "skip", "مرور درخواست"}, {"choice", "submit", "درخواست دریافت شد"}}},
		{"apply", []struct{ key, value, want string }{{"answer", "ناشناخته", "یکی از گزینه"}, {"choice", "option:1", "مقدار؟"}, {"answer", "0.99", "دست\u200cکم"}, {"answer", "10.1", "حداکثر"}, {"answer", "1e9", "عدد صحیح"}, {"answer", "۲٫۵", "مرور ثبت\u200cنام"}, {"choice", "submit", "پذیرش تضمین نمی\u200cشود"}}},
		{"book", []struct{ key, value, want string }{{"answer", "1404/12/30", "تاریخ شمسی معتبر"}, {"answer", "1404/1/1", "تاریخ باید از"}, {"answer", "1406/1/1", "تاریخ باید تا"}, {"answer", "۱۴۰۵/۷/۱۱", "مرور رزرو"}, {"choice", "submit", "رزرو قطعی نیست"}}},
	} {
		path := b.Post("/bots/1/preview", url.Values{}).Header().Get("Location")
		if got := b.Post(path+"/choose", url.Values{"choice": {tc.choice}, "revision": {"1"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		for i, step := range tc.steps {
			if got := b.Post(path+"/choose", url.Values{step.key: {step.value}, "revision": {strconv.Itoa(i + 2)}}); got.Code != 303 {
				t.Fatalf("%s step %d: %d", tc.choice, i, got.Code)
			}
			if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), step.want) {
				t.Fatalf("%s step %d missing %q", tc.choice, i, step.want)
			}
		}
	}
	if page := b.Send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created real records")
	}
}
