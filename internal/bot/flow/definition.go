// Package flow defines approved, versioned behavior without executable code.
package flow

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxChoices = 6

type Definition struct {
	Version  int     `json:"version"`
	Welcome  Block   `json:"welcome"`
	Menu     Block   `json:"menu"`
	Messages []Block `json:"messages"`
	Forms    []Form  `json:"forms,omitempty"`
}

type Form struct {
	ID              string     `json:"id"`
	Questions       []Question `json:"questions"`
	Review          string     `json:"review"`
	Acknowledgement string     `json:"acknowledgement"`
}

type Question struct {
	ID       string       `json:"id"`
	Label    string       `json:"label"`
	Prompt   string       `json:"prompt"`
	Type     string       `json:"type"`
	Required bool         `json:"required"`
	Options  []string     `json:"options,omitempty"`
	Number   *NumberRules `json:"number,omitempty"`
}

type NumberRules struct {
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
}

func (d Definition) Form(id string) (Form, bool) {
	for _, f := range d.Forms {
		if f.ID == id {
			return f, true
		}
	}
	return Form{}, false
}

type Block struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Text    string   `json:"text"`
	Choices []Choice `json:"choices,omitempty"`
}

type Choice struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Target string `json:"target"`
}

// Message resolves a configured reply; validation ensures menu targets use it.
func (d Definition) Message(id string) (string, bool) {
	for _, b := range d.Messages {
		if b.ID == id {
			return b.Text, true
		}
	}
	return "", false
}

// Invalid carries a system-copy key, never arbitrary owner text.
type Invalid struct{ Key string }

func (e *Invalid) Error() string { return e.Key }
func invalid(key string) error   { return &Invalid{Key: "draft.error." + key} }

func Decode(data string) (Definition, error) {
	var d Definition
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	// Leave room for JSON's six-byte escaping of each allowed text character.
	if len(data) > 128<<10 || decoder.Decode(&d) != nil {
		return d, invalid("definition")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return d, invalid("definition")
	}
	return d, d.Validate()
}

func text(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= limit
}

func (d Definition) Validate() error {
	if (d.Version != 1 && d.Version != 2) || d.Welcome.Type != "message" || d.Menu.Type != "menu" || (d.Version == 1 && len(d.Forms) != 0) {
		return invalid("definition")
	}
	if !text(d.Welcome.Text, 2000) || !text(d.Menu.Text, 2000) {
		return invalid("text")
	}
	if len(d.Menu.Choices) < 1 || len(d.Menu.Choices) > MaxChoices || len(d.Messages)+len(d.Forms) != len(d.Menu.Choices) {
		return invalid("choices")
	}
	ids := map[string]bool{}
	for _, b := range append([]Block{d.Welcome, d.Menu}, d.Messages...) {
		if !text(b.ID, 64) || ids[b.ID] {
			return invalid("references")
		}
		ids[b.ID] = true
	}
	if len(d.Welcome.Choices) != 0 {
		return invalid("definition")
	}
	messages := map[string]bool{}
	for _, b := range d.Messages {
		if b.Type != "message" || len(b.Choices) != 0 {
			return invalid("definition")
		}
		if !text(b.Text, 2000) {
			return invalid("text")
		}
		messages[b.ID] = true
	}
	choices, targets, labels := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range d.Forms {
		if !text(f.ID, 64) || ids[f.ID] || !text(f.Review, 2000) || !text(f.Acknowledgement, 2000) || len(f.Questions) < 1 || len(f.Questions) > 12 {
			return invalid("definition")
		}
		ids[f.ID], messages[f.ID] = true, true
		questionIDs := map[string]bool{}
		for _, q := range f.Questions {
			if !text(q.ID, 64) || questionIDs[q.ID] || !text(q.Label, 80) || !text(q.Prompt, 2000) {
				return invalid("definition")
			}
			questionIDs[q.ID] = true
			switch q.Type {
			case "short_text", "long_text", "phone":
				if len(q.Options) != 0 || q.Number != nil {
					return invalid("definition")
				}
			case "single_choice":
				if q.Number != nil || len(q.Options) < 2 || len(q.Options) > MaxChoices {
					return invalid("definition")
				}
				options := map[string]bool{}
				for _, option := range q.Options {
					if !text(option, 80) || option != strings.TrimSpace(option) || options[option] {
						return invalid("definition")
					}
					options[option] = true
				}
			case "number":
				if len(q.Options) != 0 || !validNumberRules(q.Number) {
					return invalid("definition")
				}
			default:
				return invalid("definition")
			}
		}
	}
	for _, c := range d.Menu.Choices {
		if !text(c.Label, 80) {
			return invalid("label")
		}
		if !text(c.ID, 64) || choices[c.ID] || labels[strings.TrimSpace(c.Label)] || !messages[c.Target] || targets[c.Target] {
			return invalid("references")
		}
		choices[c.ID], targets[c.Target], labels[strings.TrimSpace(c.Label)] = true, true, true
	}
	// Version 2 adds bounded, sequential Forms; neither version executes code.
	data, err := json.Marshal(d)
	if err != nil || len(data) > 128<<10 {
		return invalid("definition")
	}
	return nil
}
