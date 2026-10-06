package bot_test

import (
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestCombinedFormsSettingsRoundTripAndMenuRouting(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.CombinedDraft()); got.Code != 303 {
		t.Fatalf("combined save: %d", got.Code)
	}
	page := b.Send("GET", "/bots/1/draft", nil)
	for _, want := range []string{`value="inquiry"`, `value="registration"`, `value="booking"`, "هنر\nعلوم", `value="phone"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("settings missing %q", want)
		}
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

func TestCombinedFormsCatalogAndQuestionControlsPreserveIndependentSettings(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	v := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	edit := func(action string) {
		t.Helper()
		v.Set("edit", action)
		got := b.PostDraft(t, "/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatalf("edit %s: %d", action, got.Code)
		}
		v = fixture.RenderedDraft(t, got.Body.String())
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
	if got := b.Post("/bots/1/preview", url.Values{}); got.Code != 409 {
		t.Fatal("editing silently saved Draft")
	}
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 303 {
		t.Fatalf("save rendered settings: %d", got.Code)
	}
	loaded := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
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
	a, b := fixture.DraftFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.CombinedDraft()); got.Code != 303 {
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
			v := fixture.CombinedDraft()
			tc.change(v)
			if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 422 {
				t.Fatal(got.Code)
			}
		})
	}
	page := b.Send("GET", "/bots/1/draft", nil)
	v := fixture.RenderedDraft(t, page.Body.String())
	if !reflect.DeepEqual(v["form_id"], []string{"booking", "inquiry", "registration"}) || !strings.Contains(page.Body.String(), "نام؟") {
		t.Fatal("invalid Draft replaced saved settings")
	}
	if got := b.Send("POST", "/bots/1/draft", fixture.CombinedDraft()); got.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("combined-other@example.test", "Other", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/draft", "/bots/1/preview", "/bots/1/submissions"} {
		if got := other.Send("GET", path, nil); got.Code != 404 {
			t.Fatal("cross-owner access", path, got.Code)
		}
	}
	bad := fixture.CombinedDraft()
	bad.Set("edit", "add:inquiry")
	if got := other.PostDraft(t, "/bots/1/draft", bad); got.Code != 404 {
		t.Fatal("cross-owner edit")
	}
	if got := b.Post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal("valid saved Draft could no longer publish")
	}
}

func TestCombinedFormsAllValidatorsRunInIsolatedPreview(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	v := fixture.CombinedDraft()
	v["text_max"][0] = "3"
	v["text_max"][2] = "5"
	v["date_min"][5] = "1405/1/1"
	v["date_max"][5] = "1405/12/29"
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 303 {
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

func TestCombinedFormsEnforceEditorBoundsAndRetainMessageOptions(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.CombinedDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	v := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	for len(v["menu_order"]) < 6 {
		v.Set("edit", "add:message")
		got := b.PostDraft(t, "/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatal(got.Code)
		}
		v = fixture.RenderedDraft(t, got.Body.String())
	}
	v.Set("edit", "add:booking")
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 422 {
		t.Fatal("seventh menu option accepted")
	}
	v.Del("edit")
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 303 {
		t.Fatal(got.Code)
	}
	v = fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	if len(v["choice_id"]) != 3 || len(v["form_id"]) != 3 {
		t.Fatal("mixed menu lost Forms or messages")
	}
	for len(v["question_id"]) < 17 { // 12 Booking + 3 Inquiry + 2 Registration
		v.Set("add_type_0", "short_text")
		v.Set("edit", "question:add:0")
		got := b.PostDraft(t, "/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatal(got.Code)
		}
		v = fixture.RenderedDraft(t, got.Body.String())
	}
	v.Set("edit", "question:add:0")
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 422 {
		t.Fatal("thirteenth Question accepted")
	}
	v.Set("edit", "question:remove:0:0")
	// Removing all questions is allowed while editing, but cannot be saved.
	for range 12 {
		got := b.PostDraft(t, "/bots/1/draft", v)
		if got.Code != 200 {
			t.Fatal(got.Code)
		}
		v = fixture.RenderedDraft(t, got.Body.String())
		v.Set("edit", "question:remove:0:0")
	}
	v.Del("edit")
	if got := b.PostDraft(t, "/bots/1/draft", v); got.Code != 422 {
		t.Fatal("empty Form saved")
	}
	if page := b.Send("GET", "/bots/1/draft", nil); !strings.Contains(page.Body.String(), "تاریخ؟") {
		t.Fatal("invalid empty Form changed saved Draft")
	}
}

func TestCombinedFormsInvalidTextLimitPreservesQuestionsForCorrection(t *testing.T) {
	_, b := fixture.DraftFixture(t)
	if got := b.PostDraft(t, "/bots/1/draft", fixture.CombinedDraft()); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, raw := range []string{"0", "-1", "abc", "999999999999999999999999"} {
		v := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
		originalIDs := append([]string(nil), v["question_id"]...)
		v["text_max"][3] = raw
		v.Set("edit", "add:message")
		got := b.PostDraft(t, "/bots/1/draft", v)
		if got.Code != 422 {
			t.Fatalf("invalid %q: %d", raw, got.Code)
		}
		corrected := fixture.RenderedDraft(t, got.Body.String())
		if !reflect.DeepEqual(corrected["question_id"], originalIDs) {
			t.Fatalf("invalid limit %q removed Questions: %v", raw, corrected["question_id"])
		}
		if corrected["text_max"][3] != raw {
			t.Fatalf("lost invalid field %q", raw)
		}
		corrected["text_max"][3] = "5"
		if got := b.PostDraft(t, "/bots/1/draft", corrected); got.Code != 303 {
			t.Fatal("corrected settings save", got.Code)
		}
		loaded := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
		if !reflect.DeepEqual(loaded["question_id"], originalIDs) {
			t.Fatal("corrected save silently removed Questions")
		}
	}
}
