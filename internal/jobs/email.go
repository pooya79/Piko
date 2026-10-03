package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"buildx/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

const QueueEmail = "email"

type SendAccountEmailArgs struct {
	Nonce string `json:"nonce"`
}

func (SendAccountEmailArgs) Kind() string { return "send_account_email" }

func (SendAccountEmailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue: QueueEmail, MaxAttempts: 10,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true, ByQueue: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending,
				rivertype.JobStateRetryable, rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

type AccountMailer interface {
	MessageForChallenge(context.Context, string) (auth.EmailMessage, error)
}

type EmailSender interface {
	Send(context.Context, string, string, string) error
}

type SendAccountEmailWorker struct {
	river.WorkerDefaults[SendAccountEmailArgs]
	mailer AccountMailer
	sender EmailSender
}

func (w *SendAccountEmailWorker) Work(ctx context.Context, job *river.Job[SendAccountEmailArgs]) error {
	message, err := w.mailer.MessageForChallenge(ctx, job.Args.Nonce)
	if errors.Is(err, auth.ErrInvalidChallenge) {
		return nil // A newer request, expiry, or cancellation made this job obsolete.
	}
	if err != nil {
		return fmt.Errorf("resolve account email: %w", err)
	}
	// SMTP can accept a message before a lost acknowledgement; a retry repeats the same link.
	if err := w.sender.Send(ctx, message.To, message.Subject, message.Body); err != nil {
		return fmt.Errorf("send account email: %w", err)
	}
	return nil
}

func (*SendAccountEmailWorker) Timeout(*river.Job[SendAccountEmailArgs]) time.Duration {
	return 30 * time.Second
}

// EmailEnqueuer uses the caller's transaction so an account and its email job commit together.
type EmailEnqueuer struct{ client *river.Client[pgx.Tx] }

func NewEmailEnqueuer(pool *pgxpool.Pool, log *slog.Logger) (*EmailEnqueuer, error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: log})
	if err != nil {
		return nil, err
	}
	return &EmailEnqueuer{client: client}, nil
}

func (q *EmailEnqueuer) EnqueueChallenge(ctx context.Context, tx pgx.Tx, nonce string) error {
	_, err := q.client.InsertTx(ctx, tx, SendAccountEmailArgs{Nonce: nonce}, nil)
	return err
}
