package httpfixture

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Ordinary settings carry stable identities separately from display labels.
func CombinedDraft() url.Values {
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

// Read the ordinary browser form rather than reconstructing private settings.
func RenderedDraft(t *testing.T, body string) url.Values {
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
