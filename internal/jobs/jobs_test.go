package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type fakeStore struct {
	cleanupCalls   int
	cleanupErr     error
	challengeCalls int
}

func (s *fakeStore) DeleteExpiredSessions(context.Context) (int64, error) {
	s.cleanupCalls++
	return 2, s.cleanupErr
}

func (s *fakeStore) DeleteExpiredAccountChallenges(context.Context) (int64, error) {
	s.challengeCalls++
	return 0, nil
}

func (s *fakeStore) DeleteExpiredSignupReceipts(context.Context) (int64, error) {
	return 0, nil
}

func TestWorkerConfigRegistersQueueAndPeriodicJobs(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := workerConfig(&fakeStore{}, log, 10*time.Second)

	if got := len(cfg.PeriodicJobs); got != 1 {
		t.Fatalf("periodic jobs = %d, want 1", got)
	}
	if got := cfg.Queues[QueueMaintenance].MaxWorkers; got != 2 {
		t.Fatalf("maintenance workers = %d, want 2", got)
	}
}

func TestMaintenanceJobsAreUniqueWhileActive(t *testing.T) {
	opts := CleanupSessionsArgs{}.InsertOpts()
	if opts.Queue != QueueMaintenance || opts.MaxAttempts != 5 {
		t.Fatalf("unexpected insert options: %+v", opts)
	}
	if !opts.UniqueOpts.ByArgs || !opts.UniqueOpts.ByQueue {
		t.Fatal("maintenance job uniqueness is not scoped by args and queue")
	}
	if slices.Contains(opts.UniqueOpts.ByState, rivertype.JobStateCompleted) {
		t.Fatal("completed jobs must not suppress the next periodic run")
	}
	for _, state := range []rivertype.JobState{
		rivertype.JobStateAvailable,
		rivertype.JobStatePending,
		rivertype.JobStateRetryable,
		rivertype.JobStateRunning,
		rivertype.JobStateScheduled,
	} {
		if !slices.Contains(opts.UniqueOpts.ByState, state) {
			t.Fatalf("active state %q is not unique", state)
		}
	}
}

func TestCleanupSessionsWorkerWrapsStoreError(t *testing.T) {
	want := errors.New("database unavailable")
	store := &fakeStore{cleanupErr: want}
	worker := &CleanupSessionsWorker{
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		store: store,
	}

	err := worker.Work(context.Background(), &river.Job[CleanupSessionsArgs]{})
	if !errors.Is(err, want) {
		t.Fatalf("Work() error = %v, want wrapped store error", err)
	}
	if store.cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", store.cleanupCalls)
	}
}
