package bot

import (
	"context"
	"errors"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrPauseUnavailable = errors.New("pause requires an activated published Bot in this delivery mode")

func (s *Service) SetPaused(ctx context.Context, botID int64, paused bool) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	if _, err := s.Get(ctx, botID); err != nil {
		return err
	}
	return s.setPaused(ctx, s.repo.q, ownerID, botID, paused)
}

func (s *Service) setPaused(ctx context.Context, q *dbgen.Queries, ownerID, botID int64, paused bool) error {
	value := int64(0)
	if paused {
		value = 1
	}
	// This immediate write serializes with inbox staging. Already committed
	// transitions keep their durable outputs; subsequent transitions see pause.
	n, err := q.SetOwnerBotPaused(ctx, dbgen.SetOwnerBotPausedParams{OwnerID: ownerID, ID: botID, Paused: value, Mode: string(s.deliveryMode())})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPauseUnavailable
	}
	return nil
}
