package app

import (
	"github.com/pooya79/Piko/internal/bot/telegram"
	"golang.org/x/net/html"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Ordinary settings carry stable identities separately from display labels.
func combinedDraft() url.Values {
	return url.Values{
		"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"},
		"form_id":           {"inquiry", "registration", "booking"},
		"form_choice_id":    {"ask", "apply", "book"},
		"form_label":        {"درخواست", "ثبت\u200cنام", "رزرو"},
		"review_message":    {"مرور درخواست", "مرور ثبت\u200cنام", "مرور رزرو"},
		"acknowledgement":   {"درخواست دریافت شد", "درخواست ثبت\u200cنام دریافت شد؛ پذیرش تضمین نمی\u200cشود", "درخواست رزرو دریافت شد؛ رزرو قطعی نیست"},
		"question_form":     {"inquiry", "inquiry", "inquiry", "registration", "registration", "booking"},
		"question_id":       {"name", "phone", "details", "category", "amount", "date"},
		"question_type":     {"short_text", "phone", "long_text", "single_choice", "number", "date"},
		"question_label":    {"نام", "تماس", "درخواست", "دوره", "مقدار", "تاریخ"},
		"question_prompt":   {"نام؟", "تماس؟", "درخواست؟", "دوره؟", "مقدار؟", "تاریخ؟"},
		"question_required": {"yes", "yes", "no", "yes", "yes", "yes"},
		"question_options":  {"", "", "", "هنر\nعلوم", "", ""},
		"number_min":        {"", "", "", "", "1", ""}, "number_max": {"", "", "", "", "10", ""},
		"text_max": {"", "", "", "", "", ""},
		"date_min": {"", "", "", "", "", ""}, "date_max": {"", "", "", "", "", ""},
		"menu_order": {"book", "ask", "apply"},
	}
}

func TestCombinedFormsSettingsRoundTripAndMenuRouting(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", combinedDraft()); got.Code != 303 {
		t.Fatalf("combined save: %d", got.Code)
	}
	page := b.send("GET", "/bots/1/draft", nil)
	for _, want := range []string{`value="inquiry"`, `value="registration"`, `value="booking"`, "هنر\nعلوم", `value="phone"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("settings missing %q", want)
		}
	}
	for _, tc := range []struct{ choice, prompt string }{{"ask", "نام؟"}, {"apply", "دوره؟"}, {"book", "تاریخ؟"}} {
		path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
		if got := b.post(path+"/choose", url.Values{"choice": {tc.choice}, "revision": {"1"}}); got.Code != 303 {
			t.Fatalf("route %s: %d", tc.choice, got.Code)
		}
		if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), tc.prompt) {
			t.Fatalf("route %s missing %s", tc.choice, tc.prompt)
		}
	}
}

// Read the ordinary browser form rather than reconstructing private settings.
func renderedDraft(t *testing.T, body string) url.Values {
	t.Helper()
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	var content func(*html.Node) string
	content = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		value := ""
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			value += content(c)
		}
		return value
	}
	values := url.Values{}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inside bool) {
		if n.Data == "form" {
			inside = attr(n, "class") == "piko-draft-form"
		}
		if inside && attr(n, "name") != "" {
			name := attr(n, "name")
			switch n.Data {
			case "input":
				values.Add(name, attr(n, "value"))
			case "textarea":
				values.Add(name, content(n))
			case "select":
				value := ""
				first := true
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Data != "option" {
						continue
					}
					selected := false
					for _, a := range c.Attr {
						if a.Key == "selected" {
							selected = true
						}
					}
					if first || selected {
						value = attr(c, "value")
						first = false
					}
				}
				values.Add(name, value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inside)
		}
	}
	walk(root, false)
	if values.Get("csrf_token") == "" {
		t.Fatal("missing browser Draft form")
	}
	return values
}

func TestCombinedFormsCatalogAndQuestionControlsPreserveIndependentSettings(t *testing.T) {
	_, b := draftFixture(t)
	v := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	edit := func(action string) {
		t.Helper()
		v.Set("edit", action)
		got := b.post("/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatalf("edit %s: %d", action, got.Code)
		}
		v = renderedDraft(t, got.Body.String())
	}
	edit("add:inquiry")
	v["question_prompt"][0] = "نام سفارشی؟"
	edit("add:registration")
	edit("add:booking")
	// Reorder the menu before editing a Form: action indexes follow what is shown.
	edit("menu:up:2")
	if !reflect.DeepEqual(v["menu_order"], []string{"inquiry", "booking", "registration"}) {
		t.Fatal("menu reorder lost routes")
	}
	for _, kind := range []string{"short_text", "long_text", "phone", "single_choice", "date", "number"} {
		v.Set("add_type_1", kind)
		edit("question:add:1")
	}
	// Move the last added number to the first question, then remove an original.
	for index := 8; index > 0; index-- {
		edit("question:up:1:" + strconv.Itoa(index))
	}
	edit("question:remove:1:1")
	bookingTypes := []string{}
	for i, id := range v["question_form"] {
		if id == "booking" {
			bookingTypes = append(bookingTypes, v["question_type"][i])
		}
	}
	if !reflect.DeepEqual(bookingTypes, []string{"number", "date", "long_text", "short_text", "long_text", "phone", "single_choice", "date"}) {
		t.Fatalf("question reorder: %v", bookingTypes)
	}
	if v["question_prompt"][0] != "نام سفارشی؟" {
		t.Fatal("catalog replaced earlier configuration")
	}
	// Nothing has been saved yet.
	if got := b.post("/bots/1/preview", url.Values{}); got.Code != 409 {
		t.Fatal("editing silently saved Draft")
	}
	if got := b.post("/bots/1/draft", v); got.Code != 303 {
		t.Fatalf("save rendered settings: %d", got.Code)
	}
	loaded := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	for _, key := range []string{"menu_order", "form_id", "question_id", "question_form", "question_type", "question_prompt"} {
		if !reflect.DeepEqual(v[key], loaded[key]) {
			t.Fatalf("round-trip %s: %v / %v", key, v[key], loaded[key])
		}
	}
	// Two independently configured instances of the same Template get distinct IDs.
	v = loaded
	edit("add:inquiry")
	if v["form_id"][3] != "inquiry-2" || v["question_prompt"][0] != "نام سفارشی؟" {
		t.Fatal("repeated Template overwrote its first Form")
	}
	edit("menu:remove:3")
	if len(v["form_id"]) != 3 {
		t.Fatal("remove did not remove matching Form")
	}
}

func TestCombinedFormsRejectMalformedSettingsAndDefinitionsWithoutReplacingDraft(t *testing.T) {
	a, b := draftFixture(t)
	if got := b.post("/bots/1/draft", combinedDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, tc := range []struct {
		name   string
		change func(url.Values)
	}{
		{"duplicate Form", func(v url.Values) { v["form_id"][1] = "inquiry" }},
		{"duplicate choice", func(v url.Values) { v["form_choice_id"][1] = "ask" }},
		{"unknown question Form", func(v url.Values) { v["question_form"][0] = "missing" }},
		{"duplicate question", func(v url.Values) { v["question_id"][1] = "name" }},
		{"unknown type", func(v url.Values) { v["question_type"][0] = "script" }},
		{"missing fields", func(v url.Values) { v.Del("question_type") }},
		{"duplicate menu reference", func(v url.Values) { v["menu_order"][0] = "ask" }},
		{"missing menu reference", func(v url.Values) { v["menu_order"][0] = "missing" }},
		{"short text bound", func(v url.Values) { v["text_max"][0] = "201" }},
		{"negative bound", func(v url.Values) { v["text_max"][0] = "-1" }},
		{"long text bound", func(v url.Values) { v["text_max"][2] = "2001" }},
		{"phone text rules", func(v url.Values) { v["text_max"][1] = "10" }},
		{"duplicate options", func(v url.Values) { v["question_options"][3] = "هنر\nهنر" }},
		{"number bounds", func(v url.Values) { v["number_min"][4] = "11" }},
		{"bad Jalali bound", func(v url.Values) { v["date_min"][5] = "1404/12/30" }},
		{"reversed dates", func(v url.Values) { v["date_min"][5] = "1405/1/2"; v["date_max"][5] = "1405/1/1" }},
		{"date rule on text", func(v url.Values) { v["date_min"][0] = "1405/1/1" }},
		{"too many Forms", func(v url.Values) { v["form_id"] = append(v["form_id"], "a", "b", "c", "d") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := combinedDraft()
			tc.change(v)
			if got := b.post("/bots/1/draft", v); got.Code != 422 {
				t.Fatal(got.Code)
			}
		})
	}
	page := b.send("GET", "/bots/1/draft", nil)
	v := renderedDraft(t, page.Body.String())
	if !reflect.DeepEqual(v["form_id"], []string{"booking", "inquiry", "registration"}) || !strings.Contains(page.Body.String(), "نام؟") {
		t.Fatal("invalid Draft replaced saved settings")
	}
	if got := b.send("POST", "/bots/1/draft", combinedDraft()); got.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("combined-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/draft", "/bots/1/preview", "/bots/1/submissions"} {
		if got := other.send("GET", path, nil); got.Code != 404 {
			t.Fatal("cross-owner access", path, got.Code)
		}
	}
	bad := combinedDraft()
	bad.Set("edit", "add:inquiry")
	if got := other.post("/bots/1/draft", bad); got.Code != 404 {
		t.Fatal("cross-owner edit")
	}
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal("valid saved Draft could no longer publish")
	}
}

func TestCombinedFormsAllValidatorsRunInIsolatedPreview(t *testing.T) {
	_, b := draftFixture(t)
	v := combinedDraft()
	v["text_max"][0] = "3"
	v["text_max"][2] = "5"
	v["date_min"][5] = "1405/1/1"
	v["date_max"][5] = "1405/12/29"
	if got := b.post("/bots/1/draft", v); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, tc := range []struct {
		choice string
		steps  []struct{ key, value, want string }
	}{
		{"ask", []struct{ key, value, want string }{{"answer", " ", "الزامی"}, {"answer", "چهار", "بیش از اندازه"}, {"answer", "مینا", "بیش از اندازه"}, {"answer", "سار", "تماس؟"}, {"answer", "abc", "شماره تلفن معتبر"}, {"answer", "۰۹۱۲۳۴۵۶۷۸۹", "درخواست؟"}, {"answer", "بسیار بلند", "بیش از اندازه"}, {"choice", "skip", "مرور درخواست"}, {"choice", "submit", "درخواست دریافت شد"}}},
		{"apply", []struct{ key, value, want string }{{"answer", "ناشناخته", "یکی از گزینه"}, {"choice", "option:1", "مقدار؟"}, {"answer", "0.99", "دست\u200cکم"}, {"answer", "10.1", "حداکثر"}, {"answer", "1e9", "عدد صحیح"}, {"answer", "۲٫۵", "مرور ثبت\u200cنام"}, {"choice", "submit", "پذیرش تضمین نمی\u200cشود"}}},
		{"book", []struct{ key, value, want string }{{"answer", "1404/12/30", "تاریخ شمسی معتبر"}, {"answer", "1404/1/1", "تاریخ باید از"}, {"answer", "1406/1/1", "تاریخ باید تا"}, {"answer", "۱۴۰۵/۷/۱۱", "مرور رزرو"}, {"choice", "submit", "رزرو قطعی نیست"}}},
	} {
		path := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
		if got := b.post(path+"/choose", url.Values{"choice": {tc.choice}, "revision": {"1"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		for i, step := range tc.steps {
			if got := b.post(path+"/choose", url.Values{step.key: {step.value}, "revision": {strconv.Itoa(i + 2)}}); got.Code != 303 {
				t.Fatalf("%s step %d: %d", tc.choice, i, got.Code)
			}
			if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), step.want) {
				t.Fatalf("%s step %d missing %q", tc.choice, i, step.want)
			}
		}
	}
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("Preview created real records")
	}
}

func TestCombinedFormsTelegramCompletesEachRouteAndPinsReorderedQuestionsAcrossPublication(t *testing.T) {
	d := newFormDriver(t, combinedDraft())
	stop := runDeliveryApp(t, d.a)
	d.text("/start", 2)
	d.press("درخواست", 1)
	d.text("مینا", 1)
	changed := renderedDraft(t, d.b.send("GET", "/bots/1/draft", nil).Body.String())
	// The Inquiry Form is second in the rendered menu. Reorder it and relabel
	// a question in a later version while the live Participant is answering it.
	changed.Set("edit", "question:up:1:2")
	got := d.b.post("/bots/1/draft", changed)
	if got.Code != 200 {
		t.Fatal(got.Code)
	}
	changed = renderedDraft(t, got.Body.String())
	changed["question_prompt"][1] = "نام تازه؟"
	changed["question_label"][1] = "نام تازه"
	if got := d.b.post("/bots/1/draft", changed); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := d.b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	stop()
	restarted, err := newWithTelegram(t.Context(), d.a.cfg, telegram.NewClient(d.f.url, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	d.a, d.b.router = restarted, restarted.server.Handler
	runDeliveryApp(t, restarted)
	d.text("/start", 1)
	d.press("ادامه", 1)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "تماس؟" {
		t.Fatal("in-progress Form changed version/order")
	}
	d.text("۰۹۱۲۳۴۵۶۷۸۹", 1)
	d.text("درخواست قدیمی", 4)
	d.press("ارسال", 1)
	d.countSubmissions(1)
	d.press("شروع دوباره", 2)
	d.press("ثبت\u200cنام", 1)
	d.press("علوم", 1)
	d.text("۳٫۵", 3)
	d.press("ارسال", 1)
	d.countSubmissions(2)
	if sent := waitSent(t, d.f, d.sent); !strings.Contains(sent[len(sent)-1].Text, "پذیرش تضمین نمی\u200cشود") {
		t.Fatal("Registration receipt semantics")
	}
	d.press("شروع دوباره", 2)
	d.press("رزرو", 1)
	d.text("۱۴۰۵/۷/۱۱", 2)
	d.press("ارسال", 1)
	d.countSubmissions(3)
	if sent := waitSent(t, d.f, d.sent); !strings.Contains(sent[len(sent)-1].Text, "رزرو قطعی نیست") {
		t.Fatal("Booking receipt semantics")
	}
	for _, tc := range []struct {
		id     int
		values []string
	}{{1, []string{"مینا", "09123456789", "درخواست قدیمی"}}, {2, []string{"علوم", "3.5"}}, {3, []string{"۱۴۰۵/۰۷/۱۱"}}} {
		page := d.b.send("GET", "/bots/1/submissions/"+strconv.Itoa(tc.id), nil)
		for _, want := range tc.values {
			if !strings.Contains(page.Body.String(), want) {
				t.Fatalf("Submission %d missing %s", tc.id, want)
			}
		}
	}
	d.press("شروع دوباره", 2)
	d.press("درخواست", 1)
	d.text("سارا", 1)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "درخواست؟" {
		t.Fatal("new Interaction did not follow reordered questions")
	}
}

func TestCombinedFormsDraftAndPreviewStayIsolatedFromLive(t *testing.T) {
	d := newFormDriver(t, combinedDraft())
	runDeliveryApp(t, d.a)
	old := d.b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	changed := combinedDraft()
	changed["question_prompt"][0] = "نام پیش\u200cنویس تازه؟"
	if got := d.b.post("/bots/1/draft", changed); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fresh := d.b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	for _, tc := range []struct{ path, prompt string }{{old, "نام؟"}, {fresh, "نام پیش\u200cنویس تازه؟"}} {
		if got := d.b.post(tc.path+"/choose", url.Values{"choice": {"ask"}, "revision": {"1"}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		page := d.b.send("GET", tc.path, nil)
		if !strings.Contains(page.Body.String(), tc.prompt) {
			t.Fatal("Preview snapshot lost", tc.prompt)
		}
	}
	d.text("/start", 2)
	d.press("درخواست", 1)
	if sent := waitSent(t, d.f, d.sent); sent[len(sent)-1].Text != "نام؟" {
		t.Fatal("Draft changed live Form before publication")
	}
	d.countSubmissions(0)
	other := newAccountBrowser(t, d.a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("combined-preview-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{old, fresh} {
		if got := other.send("GET", path, nil); got.Code != 404 {
			t.Fatal("Preview owner isolation")
		}
		if got := other.post(path+"/choose", url.Values{"answer": {"داده دیگر"}, "revision": {"2"}}); got.Code != 404 {
			t.Fatal("Preview mutation owner isolation")
		}
	}
}

func TestCombinedFormsEnforceEditorBoundsAndRetainMessageOptions(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", combinedDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	v := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	for len(v["menu_order"]) < 6 {
		v.Set("edit", "add:message")
		got := b.post("/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatal(got.Code)
		}
		v = renderedDraft(t, got.Body.String())
	}
	v.Set("edit", "add:booking")
	if got := b.post("/bots/1/draft", v); got.Code != 422 {
		t.Fatal("seventh menu option accepted")
	}
	v.Del("edit")
	if got := b.post("/bots/1/draft", v); got.Code != 303 {
		t.Fatal(got.Code)
	}
	v = renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if len(v["choice_id"]) != 3 || len(v["form_id"]) != 3 {
		t.Fatal("mixed menu lost Forms or messages")
	}
	for len(v["question_id"]) < 17 { // 12 Booking + 3 Inquiry + 2 Registration
		v.Set("add_type_0", "short_text")
		v.Set("edit", "question:add:0")
		got := b.post("/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatal(got.Code)
		}
		v = renderedDraft(t, got.Body.String())
	}
	v.Set("edit", "question:add:0")
	if got := b.post("/bots/1/draft", v); got.Code != 422 {
		t.Fatal("thirteenth Question accepted")
	}
	v.Set("edit", "question:remove:0:0")
	// Removing all questions is allowed while editing, but cannot be saved.
	for range 12 {
		got := b.post("/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatal(got.Code)
		}
		v = renderedDraft(t, got.Body.String())
		v.Set("edit", "question:remove:0:0")
	}
	v.Del("edit")
	if got := b.post("/bots/1/draft", v); got.Code != 422 {
		t.Fatal("empty Form saved")
	}
	if page := b.send("GET", "/bots/1/draft", nil); !strings.Contains(page.Body.String(), "تاریخ؟") {
		t.Fatal("invalid empty Form changed saved Draft")
	}
}

func TestCombinedFormsInvalidTextLimitPreservesQuestionsForCorrection(t *testing.T) {
	_, b := draftFixture(t)
	if got := b.post("/bots/1/draft", combinedDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, raw := range []string{"0", "-1", "abc", "999999999999999999999999"} {
		v := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
		originalIDs := append([]string(nil), v["question_id"]...)
		v["text_max"][3] = raw
		v.Set("edit", "add:message")
		got := b.post("/bots/1/draft", v)
		if got.Code != 422 {
			t.Fatalf("invalid %q: %d", raw, got.Code)
		}
		corrected := renderedDraft(t, got.Body.String())
		if !reflect.DeepEqual(corrected["question_id"], originalIDs) {
			t.Fatalf("invalid limit %q removed Questions: %v", raw, corrected["question_id"])
		}
		if corrected["text_max"][3] != raw {
			t.Fatalf("lost invalid field %q", raw)
		}
		corrected["text_max"][3] = "5"
		if got := b.post("/bots/1/draft", corrected); got.Code != 303 {
			t.Fatal("corrected settings save", got.Code)
		}
		loaded := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
		if !reflect.DeepEqual(loaded["question_id"], originalIDs) {
			t.Fatal("corrected save silently removed Questions")
		}
	}
}
