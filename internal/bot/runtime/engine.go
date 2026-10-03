// Package runtime interprets approved Blocks. Delivery adapters consume output;
// the engine knows nothing about Telegram, Preview storage, or Templates.
package runtime

import (
	"errors"
	"github.com/pooya79/Piko/internal/bot/flow"
)

var ErrChoice = errors.New("choice is not available")

type Output struct {
	Messages      []string
	Choices       []flow.Choice
	SelectedLabel string
}

func Start(d flow.Definition) (Output, error) {
	if err := d.Validate(); err != nil {
		return Output{}, err
	}
	return Output{Messages: []string{d.Welcome.Text, d.Menu.Text}, Choices: d.Menu.Choices}, nil
}

func Choose(d flow.Definition, choiceID string) (Output, error) {
	if err := d.Validate(); err != nil {
		return Output{}, err
	}
	for _, c := range d.Menu.Choices {
		if c.ID != choiceID {
			continue
		}
		if message, ok := d.Message(c.Target); ok {
			return Output{SelectedLabel: c.Label, Messages: []string{message, d.Menu.Text}, Choices: d.Menu.Choices}, nil
		}
	}
	return Output{}, ErrChoice
}
