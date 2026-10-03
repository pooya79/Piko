// Package preview simulates delivery of shared-engine output using only test state.
package preview

import (
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/runtime"
)

type Message struct {
	Participant bool   `json:"participant"`
	Text        string `json:"text"`
}

type Conversation struct {
	State         runtime.State `json:"state"`
	AcceptsAnswer bool          `json:"accepts_answer"`
	Messages      []Message     `json:"messages"`
	Choices       []flow.Choice `json:"choices"`
}

func Start(d flow.Definition) (Conversation, error) {
	output, err := runtime.Start(d)
	if err != nil {
		return Conversation{}, err
	}
	return deliver(Conversation{}, output), nil
}

func Choose(d flow.Definition, state Conversation, choice string) (Conversation, error) {
	output, err := runtime.Advance(d, state.State, runtime.Input{Action: choice})
	if err != nil {
		return Conversation{}, err
	}
	return deliver(state, output), nil
}

func Answer(d flow.Definition, state Conversation, text string) (Conversation, error) {
	output, err := runtime.Advance(d, state.State, runtime.Input{Text: text, Answer: true})
	if err != nil {
		return Conversation{}, err
	}
	return deliver(state, output), nil
}

func deliver(state Conversation, output runtime.Output) Conversation {
	if output.SelectedLabel != "" {
		state.Messages = append(state.Messages, Message{Participant: true, Text: output.SelectedLabel})
	}
	for _, text := range output.Messages {
		state.Messages = append(state.Messages, Message{Text: text})
	}
	// Keep the simulated transcript bounded even when an owner repeatedly clicks.
	if len(state.Messages) > 32 {
		state.Messages = append([]Message(nil), state.Messages[len(state.Messages)-32:]...)
	}
	state.Choices = output.Choices
	state.State = output.State
	state.AcceptsAnswer = output.AcceptsAnswer
	return state
}
