package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: log})
	if err != nil {
		return fmt.Errorf("create River migrator: %w", err)
	}
	result, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate River schema: %w", err)
	}
	log.InfoContext(ctx, "River schema migrated", "versions_applied", len(result.Versions))
	return nil
}
