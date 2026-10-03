// Package registration constructs applications using shared approved Blocks.
package registration

import "github.com/pooya79/Piko/internal/bot/flow"

type Settings struct {
	Welcome, MenuPrompt, Label, Review, Acknowledgement string
	Questions                                           []flow.Question
}

func Default() Settings {
	return Settings{Welcome: "سلام! درخواست ثبت\u200cنام خود را از منو آغاز کنید.", MenuPrompt: "چه کاری می\u200cخواهید انجام دهید؟", Label: "درخواست ثبت\u200cنام", Review: "پاسخ\u200cها را بررسی و درخواست ثبت\u200cنام را ارسال کنید.", Acknowledgement: "درخواست ثبت\u200cنام شما دریافت شد؛ پذیرش یا ظرفیت تضمین نمی\u200cشود.", Questions: []flow.Question{
		{ID: "name", Label: "نام", Prompt: "نام شما چیست؟", Type: "short_text", Required: true},
		{ID: "service", Label: "رویداد یا خدمت", Prompt: "کدام رویداد یا خدمت را انتخاب می\u200cکنید؟", Type: "single_choice", Required: true, Options: []string{"دوره مقدماتی", "دوره پیشرفته"}},
		{ID: "quantity", Label: "مقدار درخواستی", Prompt: "مقدار درخواستی را وارد کنید.", Type: "number", Required: true, Number: &flow.NumberRules{Min: "1"}},
	}}
}

func (s Settings) Definition() flow.Definition {
	return flow.Definition{Version: 2, Welcome: flow.Block{ID: "welcome", Type: "message", Text: s.Welcome}, Menu: flow.Block{ID: "menu", Type: "menu", Text: s.MenuPrompt, Choices: []flow.Choice{{ID: "registration", Label: s.Label, Target: "registration"}}}, Forms: []flow.Form{{ID: "registration", Questions: s.Questions, Review: s.Review, Acknowledgement: s.Acknowledgement}}}
}
