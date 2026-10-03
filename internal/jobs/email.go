package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

type AccountMailer interface {
	MessageForChallenge(context.Context, string) (auth.EmailMessage, error)
}

type EmailSender interface {
	Send(context.Context, string, string, string) error
}

// EmailEnqueuer shares the account transaction so a job cannot outlive a rollback.
type EmailEnqueuer struct{}

func NewEmailEnqueuer() *EmailEnqueuer { return &EmailEnqueuer{} }

func (*EmailEnqueuer) EnqueueChallenge(ctx context.Context, tx *sql.Tx, nonce string) error {
	return dbgen.New(tx).EnqueueAccountEmail(ctx, nonce)
}

func sendAccountEmail(ctx context.Context, mailer AccountMailer, sender EmailSender, nonce string) error {
	message, err := mailer.MessageForChallenge(ctx, nonce)
	if errors.Is(err, auth.ErrInvalidChallenge) {
		return nil // Superseded, expired, or cancelled links need no delivery.
	}
	if err != nil {
		return fmt.Errorf("resolve account email: %w", err)
	}
	// A lost SMTP acknowledgement can cause a retry; every retry uses the same link.
	if err := sender.Send(ctx, message.To, message.Subject, message.Body); err != nil {
		return fmt.Errorf("send account email: %w", err)
	}
	return nil
}
