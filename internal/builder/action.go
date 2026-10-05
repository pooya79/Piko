package builder

import (
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
	"github.com/pooya79/Piko/internal/auth"
)

type actionPreview struct {
	Action           string `json:"action"`
	BotName          string `json:"bot_name"`
	DraftRevision    int64  `json:"draft_revision"`
	PublishedVersion int64  `json:"published_version"`
	DeliveryState    string `json:"delivery_state"`
	Paused           bool   `json:"paused"`
	Available        bool   `json:"available"`
}

type actionInput struct {
	Action string `json:"action" jsonschema:"enum=deploy,enum=pause,enum=resume,description=Propose one operation for this Bot. Never executes it; owner confirmation is required."`
}

func (s *Service) actionTool() ai.ToolRef {
	return genkit.DefineTool(s.genkit, "propose_action", "Prepare one Deploy, Pause or Resume card for this Bot and its current Draft/lifecycle. No live operation is executed. Do not request tokens or combine with Draft changes. Unavailable operations provide dedicated management guidance.", func(ctx *ai.ToolContext, input actionInput) (actionPreview, error) {
		c, err := candidateFromContext(ctx)
		if err != nil {
			return actionPreview{}, err
		}
		run, ok := ctx.Value(runContextKey{}).(admittedRun)
		if !ok || run.botID == 0 {
			return actionPreview{}, ErrUnavailable
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.action != nil || c.staged != nil {
			c.invalid = true
			return actionPreview{}, ErrUnavailable
		}
		p, err := s.bots.PrepareAction(auth.WithUser(ctx, auth.User{ID: run.ownerID}), run.botID, input.Action)
		if err != nil {
			c.invalid = true
			return actionPreview{}, err
		}
		c.action = &p
		return actionPreview{Action: p.Action, BotName: p.BotName, DraftRevision: p.DraftRevision, PublishedVersion: p.PublishedVersion, DeliveryState: p.DeliveryState, Paused: p.Paused, Available: p.CanConfirm()}, nil
	})
}
