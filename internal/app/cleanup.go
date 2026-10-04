package app

import (
	"context"
	"time"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

// Cleanup shares the configured immediate transaction so partial failures roll back.
func (a *App) cleanup(ctx context.Context) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	if _, err := q.DeleteExpiredSessions(ctx); err != nil {
		return err
	}
	if _, err := q.DeleteExpiredRateLimits(ctx); err != nil {
		return err
	}
	if _, err := q.DeleteExpiredPreviews(ctx); err != nil {
		return err
	}
	if _, err := q.DeleteExpiredParticipants(ctx, a.now().Unix()); err != nil {
		return err
	}
	if _, err := q.CompactCompletedUpdateOutput(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// The tick input keeps periodic cleanup testable without an hour-long wait.
func (a *App) cleanupLoop(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if ctx.Err() != nil {
				return
			}
			if err := a.cleanup(ctx); err != nil && ctx.Err() == nil {
				a.log.ErrorContext(ctx, "cleanup expired records", "error", err)
			}
		}
	}
}
