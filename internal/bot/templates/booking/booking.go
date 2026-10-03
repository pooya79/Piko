// Package booking constructs requests using shared approved Blocks.
package booking

import "github.com/pooya79/Piko/internal/bot/flow"

type Settings struct {
	Welcome, MenuPrompt, Label, Review, Acknowledgement string
	Questions                                           []flow.Question
}

func Default() Settings {
	return Settings{Welcome: "سلام! درخواست رزرو خود را از منو آغاز کنید.", MenuPrompt: "چه کاری می\u200cخواهید انجام دهید؟", Label: "درخواست رزرو", Review: "تاریخ ترجیحی و جزئیات را بررسی و درخواست را ارسال کنید؛ رزرو قطعی نیست.", Acknowledgement: "درخواست رزرو شما دریافت شد و برای بررسی ارسال شد؛ رزرو قطعی نیست.", Questions: []flow.Question{
		{ID: "name", Label: "نام", Prompt: "نام شما چیست؟", Type: "short_text", Required: true},
		{ID: "date", Label: "تاریخ ترجیحی", Prompt: "تاریخ ترجیحی شمسی را به صورت سال/ماه/روز وارد کنید؛ مانند ۱۴۰۵/۰۷/۱۱ (تقویم تهران).", Type: "date", Required: true},
		{ID: "details", Label: "جزئیات درخواست", Prompt: "جزئیات درخواست رزرو خود را بنویسید.", Type: "long_text", Required: true},
	}}
}

func (s Settings) Definition() flow.Definition {
	return flow.Definition{Version: 2, Welcome: flow.Block{ID: "welcome", Type: "message", Text: s.Welcome}, Menu: flow.Block{ID: "menu", Type: "menu", Text: s.MenuPrompt, Choices: []flow.Choice{{ID: "booking", Label: s.Label, Target: "booking"}}}, Forms: []flow.Form{{ID: "booking", Questions: s.Questions, Review: s.Review, Acknowledgement: s.Acknowledgement}}}
}
