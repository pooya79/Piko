package jobs

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/testsupport"
)

type testSender struct {
	calls int
	err   error
	body  string
}

func (s *testSender) Send(_ context.Context, _, _, body string) error {
	s.calls++
	s.body = body
	return s.err
}

type testMailer struct{ err error }

func (m testMailer) MessageForChallenge(context.Context, string) (auth.EmailMessage, error) {
	return auth.EmailMessage{To: "test@example.test", Subject: "test", Body: "link"}, m.err
}
func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func enqueue(t *testing.T, db *sql.DB, nonce string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := NewEmailEnqueuer().EnqueueChallenge(context.Background(), tx, nonce); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountAndEmailCommitTogetherAndDeliverAfterReopen(t *testing.T) {
	ctx := context.Background()
	db, path := testsupport.MigratedSQLite(t, ctx)
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.NewAccountService(auth.NewAccountRepository(db, NewEmailEnqueuer()), auth.NewService(dbgen.New(db)), []byte("test-secret-at-least-thirty-two-bytes"), "https://example.test", catalog)
	if _, err := db.ExecContext(ctx, "CREATE TRIGGER reject_receipt BEFORE INSERT ON signup_receipts BEGIN SELECT RAISE(ABORT, 'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Register(ctx, "fail@example.test", "Test", "correct-password-123", "en"); err == nil {
		t.Fatal("failed receipt committed account")
	}
	for _, table := range []string{"users", "account_challenges", "email_jobs"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, error = %v", table, count, err)
		}
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_receipt"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Register(ctx, "test@example.test", "Test", "correct-password-123", "en"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	other, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	mailer := auth.NewAccountService(auth.NewAccountRepository(other, nil), auth.NewService(dbgen.New(other)), []byte("test-secret-at-least-thirty-two-bytes"), "https://example.test", catalog)
	sender := &testSender{}
	client := NewClient(other, testLog(), mailer, sender)
	if worked, err := client.workOne(ctx); err != nil || !worked {
		t.Fatalf("delivery worked = %v, error = %v", worked, err)
	}
	if sender.calls != 1 || sender.body == "" {
		t.Fatal("no email delivered")
	}
	if worked, err := client.workOne(ctx); err != nil || worked {
		t.Fatalf("completed job retried: %v, %v", worked, err)
	}
}

func TestEmailRetriesAndObsoleteChallenges(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mailErr, sendErr error
		attempts         int
		failed, removed  bool
		sends            int
	}{
		{"retry", nil, errors.New("SMTP unavailable"), 0, false, false, 1},
		{"exhausted", nil, errors.New("SMTP unavailable"), 9, true, false, 1},
		{"crashed final attempt", nil, nil, 10, true, false, 0},
		{"obsolete", auth.ErrInvalidChallenge, nil, 0, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, _ := testsupport.MigratedSQLite(t, ctx)
			enqueue(t, db, "challenge")
			if _, err := db.ExecContext(ctx, "UPDATE email_jobs SET attempts = ?", tc.attempts); err != nil {
				t.Fatal(err)
			}
			sender := &testSender{err: tc.sendErr}
			client := NewClient(db, testLog(), testMailer{err: tc.mailErr}, sender)
			if worked, err := client.workOne(ctx); err != nil || !worked {
				t.Fatalf("worked = %v, error = %v", worked, err)
			}
			if sender.calls != tc.sends {
				t.Fatalf("sends = %d", sender.calls)
			}
			var failed, lease sql.NullInt64
			var available int64
			err := db.QueryRowContext(ctx, "SELECT failed_at, lease_until, available_at FROM email_jobs").Scan(&failed, &lease, &available)
			if tc.removed {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("obsolete job retained: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if failed.Valid != tc.failed || lease.Valid || available <= time.Now().UnixMilli() {
				t.Fatal("retry or failure state incorrect")
			}
			if worked, err := client.workOne(ctx); err != nil || worked {
				t.Fatalf("unavailable job claimed: %v, %v", worked, err)
			}
		})
	}
}

func TestLeaseRecoveryAndStaleAcknowledgements(t *testing.T) {
	ctx := context.Background()
	db, path := testsupport.MigratedSQLite(t, ctx)
	other, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	enqueue(t, db, "challenge")
	enqueue(t, db, "challenge") // The identifier is unique.
	claim := func(q *dbgen.Queries, token string) (dbgen.EmailJob, error) {
		return q.ClaimAccountEmail(ctx, dbgen.ClaimAccountEmailParams{Now: time.Now().UnixMilli(), LeaseToken: sql.NullString{String: token, Valid: true}, LeaseUntil: sql.NullInt64{Int64: time.Now().Add(time.Minute).UnixMilli(), Valid: true}})
	}
	q, q2 := dbgen.New(db), dbgen.New(other)
	first, err := claim(q, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claim(q2, "second"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("active job claimed twice: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE email_jobs SET lease_until = 0"); err != nil {
		t.Fatal(err)
	}
	second, err := claim(q2, "second")
	if err != nil || second.Attempts != 2 {
		t.Fatalf("reclaim = %+v, error = %v", second, err)
	}
	if n, err := q.CompleteAccountEmail(ctx, dbgen.CompleteAccountEmailParams{ID: first.ID, LeaseToken: first.LeaseToken}); err != nil || n != 0 {
		t.Fatalf("stale ack rows = %d, error = %v", n, err)
	}
	if n, err := q2.CompleteAccountEmail(ctx, dbgen.CompleteAccountEmailParams{ID: second.ID, LeaseToken: second.LeaseToken}); err != nil || n != 1 {
		t.Fatalf("owner ack rows = %d, error = %v", n, err)
	}
}
