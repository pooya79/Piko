package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"modernc.org/sqlite"
)

type AccountRepository struct {
	pool *sql.DB
}

type accountTx struct {
	q *dbgen.Queries
}

func NewAccountRepository(pool *sql.DB) *AccountRepository {
	return &AccountRepository{pool: pool}
}

// Open configures BEGIN IMMEDIATE so account creation and session insertion
// share a serialized write transaction.
func (r *AccountRepository) withinTransaction(ctx context.Context, fn func(*accountTx) error) error {
	tx, err := r.pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&accountTx{q: dbgen.New(tx)}); err != nil {
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
