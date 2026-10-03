package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"buildx/internal/platform/database/dbgen"
	"buildx/internal/testsupport"
)

func TestSessionRevocationAndCSRFRenewalAgainstSQLite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, _ := testsupport.MigratedSQLite(t, ctx)
	q := dbgen.New(pool)
	u, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: fmt.Sprintf("session-%d@example.test", time.Now().UnixNano()), DisplayName: "Session", PasswordHash: "test-only"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.ExecContext(context.Background(), "DELETE FROM users WHERE id=$1", u.ID)
	})
	service := NewService(q)
	cookie, oldCSRF, _, err := service.NewSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	newCSRF, err := service.RenewCSRF(ctx, cookie)
	if err != nil {
		t.Fatal(err)
	}
	if service.VerifyCSRF(ctx, cookie, oldCSRF) || !service.VerifyCSRF(ctx, cookie, newCSRF) {
		t.Fatal("CSRF renewal did not replace the session token")
	}
	if err := service.Logout(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LoadSession(ctx, cookie); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked session error=%v", err)
	}
}
