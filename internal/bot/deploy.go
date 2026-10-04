package bot

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrBuilderBusy = errors.New("Bot has an active Builder run")

// Deployment keeps publication and delivery outcomes distinct, including when
// activation fails after the immutable version has already been committed.
type Deployment struct {
	Bot        Bot
	Version    int64
	Activation Activation
}

// InspectDeployment adds the busy state only to Deploy-facing pages. Ordinary
// Bot reads and Draft operations remain independent of Builder storage.
func (s *Service) InspectDeployment(ctx context.Context, botID int64) (Bot, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return Bot{}, err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return Bot{}, err
	}
	active, err := s.repo.q.ActiveOwnerBuilderBot(ctx, dbgen.ActiveOwnerBuilderBotParams{OwnerID: ownerID, BotID: sql.NullInt64{Int64: botID, Valid: true}})
	b.BuilderBusy = active > 0
	return b, err
}

func (s *Service) Deploy(ctx context.Context, botID int64, confirmation string) (Deployment, error) {
	result := Deployment{}
	version, err := s.publish(ctx, botID, true)
	if err != nil {
		return result, err
	}
	result.Version = version
	result.Bot, err = s.Get(ctx, botID)
	if err != nil {
		return result, err
	}
	if result.Bot.DeliveryState == "active" || result.Bot.DeliveryState == "paused" {
		return result, nil
	}
	result.Activation, err = s.Activate(ctx, botID, confirmation)
	return result, err
}
