package httpfixture

import (
	"net/url"
)

func RegistrationDraft() url.Values {
	v := url.Values{"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"}, "form_label": {"درخواست ثبت\u200cنام"}, "review_message": {"پاسخ\u200cها را بررسی کنید"}, "acknowledgement": {"درخواست ثبت\u200cنام دریافت شد؛ پذیرش یا ظرفیت تضمین نمی\u200cشود."}, "question_label": {"نام", "دوره", "مقدار"}, "question_prompt": {"نام شما چیست؟", "کدام دوره؟", "چه مقدار؟"}, "question_required": {"yes", "yes", "no"}}
	v["form_id"] = []string{"registration"}
	v["form_choice_id"] = []string{"registration"}
	v["question_form"] = []string{"registration", "registration", "registration"}
	v["question_id"] = []string{"name", "service", "quantity"}
	v["question_type"] = []string{"short_text", "single_choice", "number"}
	v["question_options"] = []string{"", "هنر\nعلوم", ""}
	v["number_min"] = []string{"", "", "۰٫۱"}
	v["number_max"] = []string{"", "", "9007199254740993.1234567890123456789"}
	v["text_max"] = []string{"", "", ""}
	v["date_min"] = []string{"", "", ""}
	v["date_max"] = []string{"", "", ""}
	return v
}
