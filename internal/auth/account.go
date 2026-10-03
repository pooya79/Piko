package auth

import (
	"context"
	"errors"
	"time"

	"github.com/pooya79/Piko/internal/locale"
)

var ErrAccountExists = errors.New("account already exists")

type AccountService struct {
	repo        *AccountRepository
	credentials *Service
}

type Registration struct {
	Account      User
	Cookie, CSRF string
	ExpiresAt    time.Time
}

func NewAccountService(repo *AccountRepository, credentials *Service) *AccountService {
	return &AccountService{repo: repo, credentials: credentials}
}

// Register commits the account and its initial session in one immediate transaction.
// Cookie credentials are returned only after the transaction commits.
func (s *AccountService) Register(ctx context.Context, email, name, password, language string) (Registration, error) {
	if !locale.Supported(language) {
		return Registration{}, ErrUnsupportedLanguage
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return Registration{}, err
	}
	name, err = validName(name)
	if err != nil {
		return Registration{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Registration{}, err
	}
	var registration Registration
	err = s.repo.withinTransaction(ctx, func(tx *accountTx) error {
		registration.Account, err = tx.createAccount(ctx, email, name, hash, language)
		if err != nil {
			return err
		}
		registration.Cookie, registration.CSRF, registration.ExpiresAt, err = s.credentials.newSession(ctx, tx.q, registration.Account.ID)
		return err
	})
	if err != nil {
		return Registration{}, err
	}
	return registration, nil
}
