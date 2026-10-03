package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/testsupport"
)

func TestConcurrentVerificationConsumesLinkOnce(t *testing.T) {
	ctx := context.Background()
	db, path := testsupport.MigratedSQLite(t, ctx)
	other, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	queue := &capturedMailQueue{}
	secret := []byte("a-test-secret-that-is-long-enough-to-use")
	accounts := NewAccountService(NewAccountRepository(db, queue), NewService(dbgen.New(db)), secret, "https://example.test", accountCatalog(t))
	registration, err := accounts.Register(ctx, "concurrent@example.test", "Test", "correct-password-123", "en")
	if err != nil {
		t.Fatal(err)
	}
	message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.Fields(message.Body)[0])
	if err != nil {
		t.Fatal(err)
	}
	token := link.Query().Get("token")
	second := NewAccountService(NewAccountRepository(other, nil), NewService(dbgen.New(other)), secret, "https://example.test", accountCatalog(t))
	results := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, service := range []*AccountService{accounts, second} {
		wg.Go(func() { <-start; results <- service.verify(ctx, token, registration.Account.ID) })
	}
	close(start)
	wg.Wait()
	close(results)
	var successes, invalid int
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInvalidChallenge) {
			invalid++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || invalid != 1 {
		t.Fatalf("successes = %d, invalid = %d", successes, invalid)
	}
}

func TestExpiredSessionsReceiptsAndChallengesAreRejected(t *testing.T) {
	ctx := context.Background()
	db, _ := testsupport.MigratedSQLite(t, ctx)
	queue := &capturedMailQueue{}
	credentials := NewService(dbgen.New(db))
	accounts := NewAccountService(NewAccountRepository(db, queue), credentials, []byte("a-test-secret-that-is-long-enough-to-use"), "https://example.test", accountCatalog(t))
	registration, err := accounts.Register(ctx, "expired@example.test", "Test", "correct-password-123", "en")
	if err != nil {
		t.Fatal(err)
	}
	message, err := accounts.MessageForChallenge(ctx, queue.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.Fields(message.Body)[0])
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf, _, err := credentials.NewSession(ctx, registration.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sessions", "signup_receipts", "account_challenges"} {
		if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET expires_at = 0"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := credentials.LoadSession(ctx, cookie); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if credentials.VerifyCSRF(ctx, cookie, csrf) {
		t.Fatal("expired session accepted CSRF")
	}
	if _, err := accounts.RecentSignup(ctx, registration.Receipt); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("expired receipt: %v", err)
	}
	if err := accounts.verify(ctx, link.Query().Get("token"), registration.Account.ID); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("expired verification: %v", err)
	}
	if _, err := accounts.MessageForChallenge(ctx, queue.challenges[0]); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("expired queued email: %v", err)
	}
}
