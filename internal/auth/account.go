package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"buildx/internal/locale"
)

// Policy values live in Go; transport details and public origin vary by deployment.
const (
	verificationLifetime  = 24 * time.Hour
	resetLifetime         = time.Hour
	signupReceiptLifetime = 30 * time.Minute
	mailRequestCooldown   = time.Minute
)

type challengePurpose string

const (
	purposeVerify challengePurpose = "verify"
	purposeReset  challengePurpose = "reset"
)

var (
	ErrAccountExists    = errors.New("account already exists")
	ErrInvalidChallenge = errors.New("invalid or expired link")
)

type AccountService struct {
	repo        *AccountRepository
	credentials *Service
	secret      []byte
	publicBase  string
	catalog     *locale.Catalog
}

type Registration struct {
	Account User
	Receipt string
}

type EmailMessage struct {
	To, Subject, Body string
}

type ChallengeInfo struct {
	UserID  int64
	Email   string
	Purpose challengePurpose
}

// VerificationEvidence is the browser's pending session or signup receipt,
// plus a password when the email link is opened in another browser.
type VerificationEvidence struct {
	Session       User
	SignupReceipt string
	Password      string
}

func NewAccountService(repo *AccountRepository, credentials *Service, secret []byte, publicBase string, catalog *locale.Catalog) *AccountService {
	return &AccountService{repo: repo, credentials: credentials, secret: secret, publicBase: strings.TrimRight(publicBase, "/"), catalog: catalog}
}

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
	hash, err := hashRegistrationPassword(password)
	if err != nil {
		return Registration{}, err
	}
	receipt, err := randomValue()
	if err != nil {
		return Registration{}, fmt.Errorf("create signup receipt: %w", err)
	}
	receiptHash := tokenHash(receipt)
	expires := time.Now().Add(signupReceiptLifetime)
	var account User
	err = s.repo.withinTransaction(ctx, func(tx *accountTx) error {
		account, err = tx.createAccount(ctx, email, name, hash, language)
		if err != nil {
			return err
		}
		if err := s.createChallenge(ctx, tx, account.ID, purposeVerify, verificationLifetime); err != nil {
			return err
		}
		return tx.saveReceipt(ctx, receiptHash, account.ID, expires)
	})
	if errors.Is(err, ErrAccountExists) {
		// Every successful registration attempt gets the same opaque cookie.
		// A duplicate address receives an unbound receipt with no account privileges.
		err = s.repo.withinTransaction(ctx, func(tx *accountTx) error {
			return tx.saveReceipt(ctx, receiptHash, 0, expires)
		})
		return Registration{Receipt: receipt}, err
	}
	if err != nil {
		return Registration{}, err
	}
	return Registration{Account: account, Receipt: receipt}, nil
}

func randomValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func encodeNonce(nonce []byte) string {
	return base64.RawURLEncoding.EncodeToString(nonce)
}

func (s *AccountService) createChallenge(ctx context.Context, tx *accountTx, userID int64, purpose challengePurpose, ttl time.Duration) error {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("create account challenge: %w", err)
	}
	token := s.deriveToken(nonce, purpose)
	return tx.saveChallenge(ctx, nonce, tokenHash(token), userID, purpose, time.Now().Add(ttl))
}

func (s *AccountService) deriveToken(nonce []byte, purpose challengePurpose) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("buildx-account-" + string(purpose) + ":"))
	_, _ = mac.Write(nonce)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// RecentSignup proves this browser started the pending account without exposing
// account status in the registration response.
func (s *AccountService) RecentSignup(ctx context.Context, receipt string) (User, error) {
	if receipt == "" {
		return User{}, ErrInvalidChallenge
	}
	return s.repo.recentSignup(ctx, tokenHash(receipt))
}

func (s *AccountService) Challenge(ctx context.Context, token string) (ChallengeInfo, error) {
	if token == "" {
		return ChallengeInfo{}, ErrInvalidChallenge
	}
	v, err := s.repo.challengeByToken(ctx, tokenHash(token))
	if err != nil {
		return ChallengeInfo{}, err
	}
	return ChallengeInfo{UserID: v.UserID, Email: v.Email, Purpose: v.Purpose}, nil
}

// VerificationForm decides whether this browser must supply the account password.
func (s *AccountService) VerificationForm(ctx context.Context, token string, evidence VerificationEvidence) (ChallengeInfo, bool, error) {
	info, err := s.Challenge(ctx, token)
	if err != nil {
		return ChallengeInfo{}, false, err
	}
	if info.Purpose != purposeVerify {
		return ChallengeInfo{}, false, ErrInvalidChallenge
	}
	matched, err := s.matchesVerificationEvidence(ctx, info.UserID, evidence)
	return info, !matched, err
}

func (s *AccountService) matchesVerificationEvidence(ctx context.Context, userID int64, evidence VerificationEvidence) (bool, error) {
	if evidence.Session.ID == userID {
		return true, nil
	}
	if evidence.SignupReceipt == "" {
		return false, nil
	}
	u, err := s.RecentSignup(ctx, evidence.SignupReceipt)
	if errors.Is(err, ErrInvalidChallenge) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return u.ID == userID, nil
}

// CompleteVerification checks browser proof or the account password before
// consuming the one-use link in the account transaction.
func (s *AccountService) CompleteVerification(ctx context.Context, token string, evidence VerificationEvidence) (User, error) {
	info, requiresPassword, err := s.VerificationForm(ctx, token, evidence)
	if err != nil {
		return User{}, err
	}
	var u User
	if requiresPassword {
		u, err = s.credentials.Authenticate(ctx, info.Email, evidence.Password)
		if err != nil {
			return User{}, err
		}
		if u.ID != info.UserID {
			return User{}, ErrInvalidCredentials
		}
	} else if evidence.Session.ID == info.UserID {
		u = evidence.Session
	} else {
		u, err = s.RecentSignup(ctx, evidence.SignupReceipt)
		if err != nil {
			return User{}, err
		}
	}
	if err := s.verify(ctx, token, u.ID); err != nil {
		return User{}, err
	}
	u.Verified = true
	return u, nil
}

func (s *AccountService) verify(ctx context.Context, token string, userID int64) error {
	if token == "" || userID <= 0 {
		return ErrInvalidChallenge
	}
	return s.repo.withinTransaction(ctx, func(tx *accountTx) error {
		v, err := tx.challengeInTransaction(ctx, tokenHash(token))
		if err != nil {
			return err
		}
		if v.Purpose != purposeVerify || v.UserID != userID {
			return ErrInvalidChallenge
		}
		if err := tx.verifyEmail(ctx, userID); err != nil {
			return err
		}
		return tx.deleteChallenge(ctx, v.Nonce)
	})
}

func (s *AccountService) CancelPending(ctx context.Context, token string) error {
	if token == "" {
		return ErrInvalidChallenge
	}
	return s.repo.withinTransaction(ctx, func(tx *accountTx) error {
		v, err := tx.challengeInTransaction(ctx, tokenHash(token))
		if err != nil {
			return err
		}
		if v.Purpose != purposeVerify {
			return ErrInvalidChallenge
		}
		return tx.cancelPending(ctx, v.UserID)
	})
}

func (s *AccountService) ResendVerification(ctx context.Context, email string) error {
	return s.requestChallenge(ctx, email, purposeVerify, verificationLifetime)
}

func (s *AccountService) RequestReset(ctx context.Context, email string) error {
	return s.requestChallenge(ctx, email, purposeReset, resetLifetime)
}

func (s *AccountService) requestChallenge(ctx context.Context, email string, purpose challengePurpose, ttl time.Duration) error {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil // Public requests do not disclose whether an address is registered.
	}
	return s.repo.withinTransaction(ctx, func(tx *accountTx) error {
		account, err := tx.accountInTransaction(ctx, email)
		if errors.Is(err, errAccountNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if (purpose == purposeVerify && account.Verified) || (purpose == purposeReset && !account.Verified) {
			return nil
		}
		last, requested, err := tx.emailRequestedAt(ctx, account.ID, purpose)
		if err != nil {
			return err
		}
		if requested && time.Since(last) < mailRequestCooldown {
			return nil
		}
		if err := tx.deleteChallengeForUser(ctx, account.ID, purpose); err != nil {
			return err
		}
		return s.createChallenge(ctx, tx, account.ID, purpose, ttl)
	})
}

func (s *AccountService) Reset(ctx context.Context, token, password string) error {
	if token == "" {
		return ErrInvalidChallenge
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.repo.withinTransaction(ctx, func(tx *accountTx) error {
		v, err := tx.challengeInTransaction(ctx, tokenHash(token))
		if err != nil {
			return err
		}
		if v.Purpose != purposeReset {
			return ErrInvalidChallenge
		}
		if err := tx.changePassword(ctx, v.UserID, hash); err != nil {
			return err
		}
		if err := tx.revokeSessions(ctx, v.UserID); err != nil {
			return err
		}
		return tx.deleteChallenge(ctx, v.Nonce)
	})
}

// MessageForChallenge resolves a queued identifier only while it is the current link.
func (s *AccountService) MessageForChallenge(ctx context.Context, encodedNonce string) (EmailMessage, error) {
	nonce, err := base64.RawURLEncoding.DecodeString(encodedNonce)
	if err != nil || len(nonce) != 32 {
		return EmailMessage{}, ErrInvalidChallenge
	}
	v, err := s.repo.challengeByNonce(ctx, nonce)
	if err != nil {
		return EmailMessage{}, err
	}
	token := s.deriveToken(nonce, v.Purpose)
	encoded := url.QueryEscape(token)
	// The nonce carries no language snapshot; use the account preference read with
	// the current challenge when the worker is ready to send.
	copy := func(key string) string { return s.catalog.Text(v.Language, key) }
	if v.Purpose == purposeVerify {
		return EmailMessage{
			To: v.Email, Subject: copy("auth.mail.verify.subject"),
			Body: s.publicBase + "/verify?token=" + encoded + "\n\n" + copy("auth.mail.verify.explanation") + "\n\n" + copy("auth.mail.verify.cancel") + "\n" + s.publicBase + "/verify/cancel?token=" + encoded + "\n",
		}, nil
	}
	return EmailMessage{
		To: v.Email, Subject: copy("auth.mail.reset.subject"),
		Body: s.publicBase + "/password/reset?token=" + encoded + "\n\n" + copy("auth.mail.reset.explanation") + "\n",
	}, nil
}
