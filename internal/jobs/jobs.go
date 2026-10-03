package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"buildx/internal/platform/database/dbgen"
)

const maxEmailAttempts = 10

// Client leases durable email jobs across worker processes. SMTP runs outside
// the claim transaction, with a shorter timeout than the lease. A crashed worker
// leaves a lease that another worker can reclaim; acknowledgements are fenced
// by a fresh token so stale workers cannot complete someone else's job.
type Client struct {
	q      *dbgen.Queries
	log    *slog.Logger
	mailer AccountMailer
	sender EmailSender
}

func NewClient(db *sql.DB, log *slog.Logger, mailer AccountMailer, sender EmailSender) *Client {
	return &Client{q: dbgen.New(db), log: log, mailer: mailer, sender: sender}
}

func (c *Client) Run(ctx context.Context) error {
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	cleanup := time.NewTicker(time.Hour)
	defer cleanup.Stop()
	if err := c.cleanup(ctx); err != nil && ctx.Err() == nil {
		c.log.ErrorContext(ctx, "cleanup failed")
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := c.workOne(ctx)
		if err != nil && ctx.Err() == nil {
			c.log.ErrorContext(ctx, "email queue operation failed")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
		case <-cleanup.C:
			if err := c.cleanup(ctx); err != nil && ctx.Err() == nil {
				c.log.ErrorContext(ctx, "cleanup failed")
			}
		}
	}
}

func (c *Client) workOne(ctx context.Context) (bool, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return false, err
	}
	now := time.Now()
	job, err := c.q.ClaimAccountEmail(ctx, dbgen.ClaimAccountEmailParams{
		LeaseToken: sql.NullString{String: hex.EncodeToString(token[:]), Valid: true},
		LeaseUntil: sql.NullInt64{Int64: now.Add(time.Minute).UnixMilli(), Valid: true},
		Now:        now.UnixMilli(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim email: %w", err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if job.Attempts > maxEmailAttempts {
		// A crash during the last attempt leaves a reclaimable lease. Retire it
		// without sending again once the delivery budget has been exhausted.
		err = errors.New("email attempts exhausted")
	} else {
		err = sendAccountEmail(sendCtx, c.mailer, c.sender, job.Nonce)
	}
	cancel()
	if err == nil {
		_, err := c.q.CompleteAccountEmail(ctx, dbgen.CompleteAccountEmailParams{ID: job.ID, LeaseToken: job.LeaseToken})
		return true, err
	}
	// Avoid logging SMTP errors: they can contain account addresses or link data.
	c.log.WarnContext(ctx, "account email delivery failed", "job_id", job.ID, "attempt", job.Attempts)
	failed := sql.NullInt64{}
	if job.Attempts >= maxEmailAttempts {
		failed = sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}
	}
	delay := time.Second * time.Duration(1<<min(job.Attempts, 10))
	_, err = c.q.RetryAccountEmail(ctx, dbgen.RetryAccountEmailParams{
		ID: job.ID, LeaseToken: job.LeaseToken, FailedAt: failed,
		AvailableAt: time.Now().Add(delay).UnixMilli(),
	})
	return true, err
}

func (c *Client) cleanup(ctx context.Context) error {
	tasks := []func(context.Context) (int64, error){c.q.DeleteExpiredSessions, c.q.DeleteExpiredAccountChallenges, c.q.DeleteExpiredSignupReceipts}
	for _, task := range tasks {
		if _, err := task(ctx); err != nil {
			return err
		}
	}
	if _, err := c.q.DeleteExpiredRateLimits(ctx); err != nil {
		return err
	}
	_, err := c.q.DeleteOldFailedEmails(ctx, sql.NullInt64{Int64: time.Now().Add(-24 * time.Hour).UnixMilli(), Valid: true})
	return err
}
