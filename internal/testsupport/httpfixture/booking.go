package httpfixture

import (
	"net/url"
)

func BookingDraft() url.Values {
	v := url.Values{"welcome": {"سلام"}, "menu_prompt": {"انتخاب کنید"}, "form_label": {"درخواست رزرو"}, "review_message": {"درخواست را بررسی کنید"}, "acknowledgement": {"درخواست شما دریافت شد؛ رزرو قطعی نیست."}, "question_label": {"نام", "تاریخ ترجیحی", "جزئیات"}, "question_prompt": {"نام شما چیست؟", "تاریخ ترجیحی شما چیست؟", "جزئیات درخواست چیست؟"}, "question_required": {"yes", "yes", "no"}}
	v["form_id"] = []string{"booking"}
	v["form_choice_id"] = []string{"booking"}
	v["question_form"] = []string{"booking", "booking", "booking"}
	v["question_id"] = []string{"name", "date", "details"}
	v["question_type"] = []string{"short_text", "date", "long_text"}
	v["question_options"] = []string{"", "", ""}
	v["number_min"] = []string{"", "", ""}
	v["number_max"] = []string{"", "", ""}
	v["text_max"] = []string{"", "", ""}
	v["date_min"] = []string{"", "", ""}
	v["date_max"] = []string{"", "", ""}
	return v
}
