package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"modernc.org/sqlite"
)

var errAccountNotFound = errors.New("account not found")

type ChallengeQueue interface {
	EnqueueChallenge(context.Context, *sql.Tx, string) error
}

type AccountRepository struct {
	pool  *sql.DB
	queue ChallengeQueue
}

type accountTx struct {
	tx    *sql.Tx
	q     *dbgen.Queries
	queue ChallengeQueue
}

type accountRecord struct {
	ID       int64
	Email    string
	Verified bool
}

type challengeRecord struct {
	Nonce    []byte
	UserID   int64
	Email    string
	Language string
	Purpose  challengePurpose
}

func NewAccountRepository(pool *sql.DB, queue ChallengeQueue) *AccountRepository {
	return &AccountRepository{pool: pool, queue: queue}
}

// withinTransaction commits the account change and its email job together.
// Open uses BEGIN IMMEDIATE, so competing writers cannot both consume a link.
func (r *AccountRepository) withinTransaction(ctx context.Context, fn func(*accountTx) error) error {
	tx, err := r.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&accountTx{tx: tx, q: dbgen.New(tx), queue: r.queue}); err != nil {
		return fmt.Errorf("account transaction: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account transaction: %w", err)
	}
	return nil
}

func (t *accountTx) createAccount(ctx context.Context, email, name, passwordHash, language string) (User, error) {
	v, err := t.q.CreateUser(ctx, dbgen.CreateUserParams{Email: email, DisplayName: name, PasswordHash: passwordHash, PreferredLanguage: sql.NullString{String: language, Valid: true}})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == 2067 {
			return User{}, ErrAccountExists
		}
		return User{}, fmt.Errorf("insert account: %w", err)
	}
	return User{ID: v.ID, Email: v.Email, DisplayName: v.DisplayName, Language: v.PreferredLanguage}, nil
}

func (t *accountTx) saveReceipt(ctx context.Context, tokenHash []byte, userID int64, expires time.Time) error {
	param := dbgen.CreateSignupReceiptParams{TokenHash: tokenHash, ExpiresAt: expires.UnixMilli()}
	if userID > 0 {
		param.UserID = sql.NullInt64{Int64: userID, Valid: true}
	}
	if err := t.q.CreateSignupReceipt(ctx, param); err != nil {
		return fmt.Errorf("insert signup receipt: %w", err)
	}
	return nil
}

func (r *AccountRepository) recentSignup(ctx context.Context, tokenHash []byte) (User, error) {
	v, err := dbgen.New(r.pool).GetRecentSignup(ctx, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidChallenge
	}
	if err != nil {
		return User{}, fmt.Errorf("load signup receipt: %w", err)
	}
	return User{ID: v.ID, Email: v.Email, DisplayName: v.DisplayName, Language: v.PreferredLanguage}, nil
}

func (t *accountTx) saveChallenge(ctx context.Context, nonce, tokenHash []byte, userID int64, purpose challengePurpose, expires time.Time) error {
	if t.queue == nil {
		return errors.New("email queue unavailable")
	}
	if err := t.q.CreateAccountChallenge(ctx, dbgen.CreateAccountChallengeParams{
		Nonce: nonce, TokenHash: tokenHash, UserID: userID, Purpose: string(purpose),
		ExpiresAt: expires.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("insert account challenge: %w", err)
	}
	if err := t.q.UpsertAccountEmailRequest(ctx, dbgen.UpsertAccountEmailRequestParams{UserID: userID, Purpose: string(purpose)}); err != nil {
		return fmt.Errorf("record account email request: %w", err)
	}
	if err := t.queue.EnqueueChallenge(ctx, t.tx, encodeNonce(nonce)); err != nil {
		return fmt.Errorf("enqueue account email: %w", err)
	}
	return nil
}

func (r *AccountRepository) challengeByToken(ctx context.Context, hash []byte) (challengeRecord, error) {
	v, err := dbgen.New(r.pool).GetAccountChallengeByToken(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return challengeRecord{}, ErrInvalidChallenge
	}
	if err != nil {
		return challengeRecord{}, fmt.Errorf("load account challenge: %w", err)
	}
	return challengeRecord{Nonce: v.Nonce, UserID: v.UserID, Email: v.Email, Purpose: challengePurpose(v.Purpose)}, nil
}

func (r *AccountRepository) challengeByNonce(ctx context.Context, nonce []byte) (challengeRecord, error) {
	v, err := dbgen.New(r.pool).GetAccountChallengeByNonce(ctx, nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return challengeRecord{}, ErrInvalidChallenge
	}
	if err != nil {
		return challengeRecord{}, fmt.Errorf("load queued challenge: %w", err)
	}
	return challengeRecord{Nonce: v.Nonce, UserID: v.UserID, Email: v.Email, Language: v.PreferredLanguage, Purpose: challengePurpose(v.Purpose)}, nil
}

func (t *accountTx) challengeInTransaction(ctx context.Context, hash []byte) (challengeRecord, error) {
	v, err := t.q.GetAccountChallengeByToken(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return challengeRecord{}, ErrInvalidChallenge
	}
	if err != nil {
		return challengeRecord{}, fmt.Errorf("load account challenge: %w", err)
	}
	return challengeRecord{Nonce: v.Nonce, UserID: v.UserID, Email: v.Email, Purpose: challengePurpose(v.Purpose)}, nil
}

func (t *accountTx) accountInTransaction(ctx context.Context, email string) (accountRecord, error) {
	v, err := t.q.GetAccountByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		return accountRecord{}, errAccountNotFound
	}
	if err != nil {
		return accountRecord{}, fmt.Errorf("load account: %w", err)
	}
	return accountRecord{ID: v.ID, Email: v.Email, Verified: v.EmailVerifiedAt.Valid}, nil
}

func (t *accountTx) emailRequestedAt(ctx context.Context, userID int64, purpose challengePurpose) (time.Time, bool, error) {
	v, err := t.q.GetAccountEmailRequest(ctx, dbgen.GetAccountEmailRequestParams{UserID: userID, Purpose: string(purpose)})
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("load email cooldown: %w", err)
	}
	return time.UnixMilli(v), true, nil
}

func (t *accountTx) deleteChallengeForUser(ctx context.Context, userID int64, purpose challengePurpose) error {
	if err := t.q.DeleteAccountChallengeForUser(ctx, dbgen.DeleteAccountChallengeForUserParams{UserID: userID, Purpose: string(purpose)}); err != nil {
		return fmt.Errorf("replace account challenge: %w", err)
	}
	return nil
}

func (t *accountTx) deleteChallenge(ctx context.Context, nonce []byte) error {
	if err := t.q.DeleteAccountChallenge(ctx, nonce); err != nil {
		return fmt.Errorf("consume account challenge: %w", err)
	}
	return nil
}

func (t *accountTx) verifyEmail(ctx context.Context, userID int64) error {
	n, err := t.q.VerifyAccountEmail(ctx, userID)
	if err != nil {
		return fmt.Errorf("verify account email: %w", err)
	}
	if n != 1 {
		return ErrInvalidChallenge
	}
	return nil
}

func (t *accountTx) cancelPending(ctx context.Context, userID int64) error {
	n, err := t.q.DeletePendingAccount(ctx, userID)
	if err != nil {
		return fmt.Errorf("cancel pending account: %w", err)
	}
	if n != 1 {
		return ErrInvalidChallenge
	}
	return nil
}

func (t *accountTx) changePassword(ctx context.Context, userID int64, hash string) error {
	n, err := t.q.UpdateAccountPassword(ctx, dbgen.UpdateAccountPasswordParams{ID: userID, PasswordHash: hash})
	if err != nil {
		return fmt.Errorf("update account password: %w", err)
	}
	if n != 1 {
		return ErrInvalidChallenge
	}
	return nil
}

func (t *accountTx) revokeSessions(ctx context.Context, userID int64) error {
	if err := t.q.DeleteAccountSessions(ctx, userID); err != nil {
		return fmt.Errorf("revoke account sessions: %w", err)
	}
	return nil
}
