package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"buildx/internal/platform/database/dbgen"
)

const (
	QueueMaintenance = "maintenance"

	cleanupSessionsKind = "cleanup_sessions"
)

// Store is the database work performed by maintenance jobs.
type Store interface {
	DeleteExpiredSessions(context.Context) (int64, error)
	DeleteExpiredAccountChallenges(context.Context) (int64, error)
	DeleteExpiredSignupReceipts(context.Context) (int64, error)
}

type CleanupSessionsArgs struct{}

func (CleanupSessionsArgs) Kind() string { return cleanupSessionsKind }

func (CleanupSessionsArgs) InsertOpts() river.InsertOpts {
	return maintenanceInsertOpts()
}

type CleanupSessionsWorker struct {
	river.WorkerDefaults[CleanupSessionsArgs]
	log   *slog.Logger
	store Store
}

func (w *CleanupSessionsWorker) Work(ctx context.Context, _ *river.Job[CleanupSessionsArgs]) error {
	count, err := w.store.DeleteExpiredSessions(ctx)
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if count > 0 {
		w.log.InfoContext(ctx, "expired sessions removed", "count", count)
	}
	challenges, err := w.store.DeleteExpiredAccountChallenges(ctx)
	if err != nil {
		return fmt.Errorf("delete expired account challenges: %w", err)
	}
	if challenges > 0 {
		w.log.InfoContext(ctx, "expired account challenges removed", "count", challenges)
	}
	receipts, err := w.store.DeleteExpiredSignupReceipts(ctx)
	if err != nil {
		return fmt.Errorf("delete expired signup receipts: %w", err)
	}
	if receipts > 0 {
		w.log.InfoContext(ctx, "expired signup receipts removed", "count", receipts)
	}
	return nil
}

func (*CleanupSessionsWorker) Timeout(*river.Job[CleanupSessionsArgs]) time.Duration {
	return 30 * time.Second
}

// maintenanceInsertOpts prevents duplicate jobs while one is queued, retrying, or running.
func maintenanceInsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 5,
		Queue:       QueueMaintenance,
		UniqueOpts: river.UniqueOpts{
			ByArgs:  true,
			ByQueue: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRetryable,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

// NewClient runs the durable account-email queue and session cleanup.
func NewClient(pool *pgxpool.Pool, log *slog.Logger, softStopTimeout time.Duration, mailer AccountMailer, sender EmailSender) (*river.Client[pgx.Tx], error) {
	config := workerConfig(dbgen.New(pool), log, softStopTimeout)
	river.AddWorker(config.Workers, &SendAccountEmailWorker{mailer: mailer, sender: sender})
	config.Queues[QueueEmail] = river.QueueConfig{MaxWorkers: 2}
	client, err := river.NewClient(riverpgxv5.New(pool), config)
	if err != nil {
		return nil, fmt.Errorf("create job client: %w", err)
	}
	return client, nil
}

func workerConfig(store Store, log *slog.Logger, softStopTimeout time.Duration) *river.Config {
	workers := river.NewWorkers()
	river.AddWorker(workers, &CleanupSessionsWorker{log: log, store: store})

	return &river.Config{
		Logger:          log,
		SoftStopTimeout: softStopTimeout,
		Queues: map[string]river.QueueConfig{
			QueueMaintenance: {MaxWorkers: 2},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(time.Hour),
				func() (river.JobArgs, *river.InsertOpts) { return CleanupSessionsArgs{}, nil },
				&river.PeriodicJobOpts{ID: cleanupSessionsKind, RunOnStart: true},
			),
		},
	}
}
