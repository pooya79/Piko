package httpfixture

import (
	"net/url"
)

func InquiryDraft() url.Values {
	v := url.Values{"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"}, "form_label": {"درخواست"}, "review_message": {"پاسخ\u200cها را بررسی کنید"}, "acknowledgement": {"درخواست شما دریافت شد"}, "question_label": {"نام", "تماس", "درخواست"}, "question_prompt": {"نام شما چیست؟", "شماره تماس شما چیست؟", "درخواست شما چیست؟"}, "question_required": {"yes", "yes", "no"}}
	v["form_id"] = []string{"inquiry"}
	v["form_choice_id"] = []string{"inquiry"}
	v["question_form"] = []string{"inquiry", "inquiry", "inquiry"}
	v["question_id"] = []string{"name", "contact", "request"}
	v["question_type"] = []string{"short_text", "phone", "long_text"}
	v["question_options"] = []string{"", "", ""}
	v["number_min"] = []string{"", "", ""}
	v["number_max"] = []string{"", "", ""}
	v["text_max"] = []string{"", "", ""}
	v["date_min"] = []string{"", "", ""}
	v["date_max"] = []string{"", "", ""}
	return v
}
