package runtime

import (
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/locale"
	"strconv"
	"strings"
	"unicode/utf8"
)

// State is delivery-independent progress pinned to the caller's Flow version.
type State struct {
	FormID   string   `json:"form_id,omitempty"`
	Phase    string   `json:"phase,omitempty"`
	Question int      `json:"question,omitempty"`
	Answers  []string `json:"answers,omitempty"`
	Editing  bool     `json:"editing,omitempty"`
}

type Input struct {
	Action, Text string
	Answer       bool
	Unsupported  bool
}
type Answer struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// DisplayValue keeps dates in their Jalali calendar, with Persian digits.
// Persistence retains the normalized Latin date string for all adapters.
func (a Answer) DisplayValue() string {
	if a.Type == "date" {
		return locale.Digits(a.Value)
	}
	return a.Value
}

func Current(d flow.Definition, state State) Output {
	o := Output{State: state}
	f, ok := d.Form(state.FormID)
	if !ok || state.Phase == "" || state.Phase == "menu" {
		o.Messages = []string{d.Menu.Text}
		o.Choices = d.Menu.Choices
		return o
	}
	cancel := flow.Choice{ID: "cancel", Label: "لغو"}
	switch state.Phase {
	case "question":
		q := f.Questions[state.Question]
		o.Messages = []string{q.Prompt}
		if q.Type == "date" {
			o.Messages[0] += "\nتاریخ شمسی با تقویم تهران، به صورت سال/ماه/روز؛ مانند ۱۴۰۵/۰۷/۱۱."
		}
		o.AcceptsAnswer = true
		for i, option := range q.Options {
			o.Choices = append(o.Choices, flow.Choice{ID: "option:" + strconv.Itoa(i), Label: option})
		}
		if !q.Required {
			o.Choices = append(o.Choices, flow.Choice{ID: "skip", Label: "رد کردن"})
		}
		if state.Question > 0 || state.Editing {
			o.Choices = append(o.Choices, flow.Choice{ID: "back", Label: "بازگشت"})
		}
		o.Choices = append(o.Choices, cancel)
	case "review":
		o.Messages = []string{f.Review}
		for i, q := range f.Questions {
			value := (Answer{Type: q.Type, Value: state.Answers[i]}).DisplayValue()
			if value == "" {
				value = "بدون پاسخ"
			}
			o.Messages = append(o.Messages, q.Label+":\n"+value)
		}
		o.Choices = []flow.Choice{{ID: "submit", Label: "ارسال"}, {ID: "edit", Label: "ویرایش پاسخ\u200cها"}, {ID: "back", Label: "بازگشت"}, cancel}
	case "edit":
		o.Messages = []string{"کدام پاسخ را ویرایش می\u200cکنید؟"}
		for _, q := range f.Questions {
			o.Choices = append(o.Choices, flow.Choice{ID: "edit:" + q.ID, Label: q.Label})
		}
		o.Choices = append(o.Choices, flow.Choice{ID: "back", Label: "بازگشت"}, cancel)
	case "submitted":
		o.Messages = []string{f.Acknowledgement}
		o.Choices = []flow.Choice{{ID: "again", Label: "شروع دوباره"}}
	case "cancelled":
		o.Messages = []string{"درخواست لغو شد؛ پاسخی ارسال نشد."}
		o.Choices = []flow.Choice{{ID: "again", Label: "شروع دوباره"}}
	}
	return o
}

func Advance(d flow.Definition, state State, input Input) (Output, error) {
	if err := d.Validate(); err != nil {
		return Output{}, err
	}
	if state.Phase == "" || state.Phase == "menu" {
		for _, c := range d.Menu.Choices {
			if c.ID != input.Action || input.Answer {
				continue
			}
			if f, ok := d.Form(c.Target); ok {
				o := Current(d, State{FormID: f.ID, Phase: "question", Answers: make([]string, len(f.Questions))})
				o.SelectedLabel = c.Label
				return o, nil
			}
			o, err := Choose(d, input.Action)
			o.State = State{}
			return o, err
		}
		return Output{}, ErrChoice
	}
	f, ok := d.Form(state.FormID)
	if !ok || len(state.Answers) != len(f.Questions) || state.Question < 0 || state.Question >= len(f.Questions) {
		return Output{}, ErrChoice
	}
	// Copy before changing answers so failed persistence cannot mutate prior state.
	state.Answers = append([]string(nil), state.Answers...)
	selected := ""
	// Choice actions are resolved only at the current question. Delivery binds
	// these actions to its durable step token; Preview binds its revision.
	if state.Phase == "question" && !input.Answer && strings.HasPrefix(input.Action, "option:") {
		q := f.Questions[state.Question]
		index, err := strconv.Atoi(strings.TrimPrefix(input.Action, "option:"))
		if q.Type != "single_choice" || err != nil || index < 0 || index >= len(q.Options) || input.Action != "option:"+strconv.Itoa(index) {
			return Output{}, ErrChoice
		}
		input.Answer, input.Text = true, q.Options[index]
	}
	if input.Answer {
		if state.Phase != "question" {
			return Output{}, ErrChoice
		}
		if input.Unsupported {
			o := Current(d, state)
			o.Messages = append([]string{"فقط پاسخ متنی بفرستید یا از دکمه\u200cهای همین پرسش استفاده کنید."}, o.Messages...)
			return o, nil
		}
		q := f.Questions[state.Question]
		value, feedback := validateAnswer(q, input.Text)
		if feedback != "" {
			o := Current(d, state)
			o.Messages = append([]string{feedback}, o.Messages...)
			return o, nil
		}
		state.Answers[state.Question] = value
		selected = input.Text
		if state.Editing || state.Question == len(f.Questions)-1 {
			state.Phase = "review"
			state.Editing = false
		} else {
			state.Question++
		}
	} else {
		o := Current(d, state)
		allowed := false
		for _, c := range o.Choices {
			if c.ID == input.Action {
				allowed = true
				selected = c.Label
				break
			}
		}
		if !allowed {
			return Output{}, ErrChoice
		}
		switch input.Action {
		case "skip":
			state.Answers[state.Question] = ""
			if state.Editing || state.Question == len(f.Questions)-1 {
				state.Phase = "review"
				state.Editing = false
			} else {
				state.Question++
			}
		case "cancel":
			state.Phase = "cancelled"
			state.Answers = make([]string, len(f.Questions))
		case "back":
			if state.Phase == "edit" || state.Editing {
				state.Phase = "review"
				state.Editing = false
			} else if state.Phase == "review" {
				state.Phase = "question"
				state.Question = len(f.Questions) - 1
			} else {
				state.Question--
			}
		case "edit":
			state.Phase = "edit"
		case "submit":
			for i, q := range f.Questions {
				if _, feedback := validateAnswer(q, state.Answers[i]); feedback != "" {
					return Output{}, ErrChoice
				}
			}
			state.Phase = "submitted"
			o := Current(d, state)
			o.SelectedLabel = selected
			for i, q := range f.Questions {
				o.Confirmed = append(o.Confirmed, Answer{Label: q.Label, Value: state.Answers[i], Type: q.Type})
			}
			return o, nil
		case "again":
			o, err := Start(d)
			return o, err
		default:
			for i, q := range f.Questions {
				if input.Action == "edit:"+q.ID {
					state.Question = i
					state.Phase = "question"
					state.Editing = true
				}
			}
		}
	}
	o := Current(d, state)
	o.SelectedLabel = selected
	return o, nil
}

func validateAnswer(q flow.Question, raw string) (string, string) {
	value := strings.TrimSpace(raw)
	if value == "" {
		if q.Required {
			return "", "پاسخ به این پرسش الزامی است."
		}
		return "", ""
	}
	if !utf8.ValidString(value) {
		return "", "پاسخ متنی معتبر وارد کنید."
	}
	limit := 2000
	if q.Type == "short_text" {
		limit = 200
	}
	if utf8.RuneCountInString(value) > limit {
		return "", "پاسخ بیش از اندازه طولانی است."
	}
	if q.Type == "single_choice" {
		for _, option := range q.Options {
			if value == option {
				return value, ""
			}
		}
		return "", "یکی از گزینه\u200cهای همین پرسش را انتخاب کنید."
	}
	if q.Type == "number" {
		number, err := flow.ParseNumber(value)
		if err != nil {
			return "", "عدد صحیح یا اعشاری معتبر وارد کنید؛ از جداکننده هزارگان استفاده نکنید."
		}
		if q.Number != nil {
			if q.Number.Min != "" {
				min, _ := flow.ParseNumber(q.Number.Min)
				if number.LessThan(min) {
					return "", "عدد باید دست\u200cکم " + min.String() + " باشد."
				}
			}
			if q.Number.Max != "" {
				max, _ := flow.ParseNumber(q.Number.Max)
				if number.GreaterThan(max) {
					return "", "عدد باید حداکثر " + max.String() + " باشد."
				}
			}
		}
		return number.String(), ""
	}
	if q.Type == "date" {
		date, err := flow.ParseDate(value)
		if err != nil {
			return "", "تاریخ شمسی معتبر به صورت سال/ماه/روز وارد کنید؛ مانند ۱۴۰۵/۰۷/۱۱."
		}
		return date, ""
	}
	if q.Type == "phone" {
		value = flow.NormalizeDigits(value)
		digits := strings.TrimPrefix(value, "+")
		if len(digits) < 7 || len(digits) > 15 {
			return "", "شماره تلفن معتبر با ۷ تا ۱۵ رقم وارد کنید."
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return "", "شماره تلفن معتبر با ۷ تا ۱۵ رقم وارد کنید."
			}
		}
	}
	return value, ""
}
