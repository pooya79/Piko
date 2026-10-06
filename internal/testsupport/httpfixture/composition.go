package httpfixture

import (
	"net/url"
)

// CombinedDraft supplies three independent Forms behind one menu.
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
