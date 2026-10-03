// Package inquiry constructs a ready-made Form using shared approved Blocks.
package inquiry

import "github.com/pooya79/Piko/internal/bot/flow"

type Settings struct {
	Welcome, MenuPrompt, Label, Review, Acknowledgement string
	Questions                                           []flow.Question
}

func Default() Settings {
	return Settings{Welcome: "سلام! برای ارسال درخواست از منو شروع کنید.", MenuPrompt: "چه کاری می\u200cخواهید انجام دهید؟", Label: "ارسال درخواست", Review: "پاسخ\u200cها را بررسی و سپس ارسال کنید.", Acknowledgement: "درخواست شما دریافت شد.", Questions: []flow.Question{
		{ID: "name", Label: "نام", Prompt: "نام شما چیست؟", Type: "short_text", Required: true},
		{ID: "contact", Label: "شماره تماس", Prompt: "شماره تماس خود را وارد کنید.", Type: "phone", Required: true},
		{ID: "request", Label: "درخواست", Prompt: "درخواست خود را توضیح دهید.", Type: "long_text", Required: true},
	}}
}

func (s Settings) Definition() flow.Definition {
	return flow.Definition{Version: 2, Welcome: flow.Block{ID: "welcome", Type: "message", Text: s.Welcome}, Menu: flow.Block{ID: "menu", Type: "menu", Text: s.MenuPrompt, Choices: []flow.Choice{{ID: "inquiry", Label: s.Label, Target: "inquiry"}}}, Forms: []flow.Form{{ID: "inquiry", Questions: s.Questions, Review: s.Review, Acknowledgement: s.Acknowledgement}}}
}
