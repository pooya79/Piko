package bot

import (
	"context"
	"strconv"
	"strings"

	"github.com/pooya79/Piko/internal/bot/flow"
)

type MenuMessage struct{ Label, Message string }
type DraftSettings struct {
	Welcome, MenuPrompt string
	Choices             []MenuMessage
}

func (s DraftSettings) Definition() flow.Definition {
	d := flow.Definition{Version: 1, Welcome: flow.Block{ID: "welcome", Type: "message", Text: s.Welcome}, Menu: flow.Block{ID: "menu", Type: "menu", Text: s.MenuPrompt}}
	for i, c := range s.Choices {
		if strings.TrimSpace(c.Label) == "" && strings.TrimSpace(c.Message) == "" {
			continue
		}
		id := strconv.Itoa(i + 1)
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: id, Label: c.Label, Target: "reply-" + id})
		d.Messages = append(d.Messages, flow.Block{ID: "reply-" + id, Type: "message", Text: c.Message})
	}
	return d
}

func settings(d flow.Definition) DraftSettings {
	s := DraftSettings{Welcome: d.Welcome.Text, MenuPrompt: d.Menu.Text}
	for _, c := range d.Menu.Choices {
		if message, ok := d.Message(c.Target); ok {
			s.Choices = append(s.Choices, MenuMessage{Label: c.Label, Message: message})
		}
	}
	return s
}

func (s DraftSettings) Fields() []MenuMessage {
	fields := append([]MenuMessage(nil), s.Choices...)
	for len(fields) < flow.MaxChoices {
		fields = append(fields, MenuMessage{})
	}
	return fields
}

func (s *Service) LoadDraft(ctx context.Context, botID int64) (flow.Definition, bool, error) {
	if _, err := s.Get(ctx, botID); err != nil {
		return flow.Definition{}, false, err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return flow.Definition{}, false, err
	}
	return s.repo.loadDraft(ctx, ownerID, botID)
}

func (s *Service) SaveDraft(ctx context.Context, botID int64, d flow.Definition) error {
	if _, err := s.Get(ctx, botID); err != nil {
		return err
	}
	if err := d.Validate(); err != nil {
		return err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	return s.repo.saveDraft(ctx, ownerID, botID, d)
}
