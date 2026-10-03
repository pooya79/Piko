package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"buildx/internal/locale"
	"buildx/internal/platform/database/dbgen"
)

var ErrSessionNotFound = errors.New("session not found")
var ErrInvalidEmail = errors.New("invalid email")
var ErrInvalidName = errors.New("invalid display name")
var ErrUnsupportedLanguage = errors.New("unsupported language")

type User struct {
	ID                 int64
	Email, DisplayName string
	Verified           bool
	Language           string
}
type Session struct {
	User      User
	ExpiresAt time.Time
}
type Service struct {
	q   *dbgen.Queries
	ttl time.Duration
}

func NewService(q *dbgen.Queries) *Service { return &Service{q: q, ttl: 24 * time.Hour} }
func normalizeEmail(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	a, e := mail.ParseAddress(v)
	if e != nil || a.Address != v || len(v) > 254 {
		return "", ErrInvalidEmail
	}
	return v, nil
}
func validName(v string) (string, error) {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 80 {
		return "", ErrInvalidName
	}
	return v, nil
}

// Authenticate returns the same public error for invalid email, unknown user, and wrong password.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	email, e := normalizeEmail(email)
	if e != nil {
		return User{}, ErrInvalidCredentials
	}
	v, e := s.q.GetUserByEmail(ctx, email)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, e
	}
	if !VerifyPassword(v.PasswordHash, password) {
		return User{}, ErrInvalidCredentials
	}
	return User{ID: v.ID, Email: v.Email, DisplayName: v.DisplayName, Verified: v.EmailVerifiedAt.Valid, Language: v.PreferredLanguage}, nil
}

// token returns a bearer value for the client and a SHA-256 digest for storage.
func token() (string, []byte, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", nil, e
	}
	plain := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(plain))
	return plain, h[:], nil
}
func (s *Service) NewSession(ctx context.Context, userID int64) (cookie, csrf string, expires time.Time, err error) {
	cookie, ch, e := token()
	if e != nil {
		return "", "", time.Time{}, e
	}
	csrf, sh, e := token()
	if e != nil {
		return "", "", time.Time{}, e
	}
	expires = time.Now().Add(s.ttl)
	n, e := s.q.CreateSession(ctx, dbgen.CreateSessionParams{TokenHash: ch, ID: userID, CsrfHash: sh, ExpiresAt: expires.UnixMilli()})
	if e == nil && n == 0 {
		e = ErrSessionNotFound
	}
	return cookie, csrf, expires, e
}
func (s *Service) LoadSession(ctx context.Context, cookie string) (Session, error) {
	h := sha256.Sum256([]byte(cookie))
	v, e := s.q.GetSession(ctx, h[:])
	if errors.Is(e, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if e != nil {
		return Session{}, e
	}
	return Session{User: User{ID: v.UserID, Email: v.Email, DisplayName: v.DisplayName, Verified: v.EmailVerifiedAt.Valid, Language: v.PreferredLanguage}, ExpiresAt: time.UnixMilli(v.ExpiresAt)}, nil
}
func (s *Service) VerifyCSRF(ctx context.Context, cookie, csrf string) bool {
	if cookie == "" || csrf == "" {
		return false
	}
	h := sha256.Sum256([]byte(cookie))
	v, e := s.q.GetSession(ctx, h[:])
	if e != nil {
		return false
	}
	got := sha256.Sum256([]byte(csrf))
	return subtleCompare(got[:], v.CsrfHash)
}
func subtleCompare(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// RenewCSRF replaces the session's stored token hash, invalidating the previous token.
func (s *Service) RenewCSRF(ctx context.Context, cookie string) (string, error) {
	csrf, hash, err := token()
	if err != nil {
		return "", err
	}
	sessionHash := sha256.Sum256([]byte(cookie))
	n, err := s.q.UpdateSessionCSRF(ctx, dbgen.UpdateSessionCSRFParams{TokenHash: sessionHash[:], CsrfHash: hash})
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", ErrSessionNotFound
	}
	return csrf, nil
}
func (s *Service) Logout(ctx context.Context, cookie string) error {
	if cookie == "" {
		return nil
	}
	h := sha256.Sum256([]byte(cookie))
	return s.q.DeleteSession(ctx, h[:])
}

// SetLanguage accepts only catalog languages before persisting a preference.
func (s *Service) SetLanguage(ctx context.Context, userID int64, language string) error {
	if !locale.Supported(language) {
		return ErrUnsupportedLanguage
	}
	n, err := s.q.UpdateUserLanguage(ctx, dbgen.UpdateUserLanguageParams{ID: userID, PreferredLanguage: language})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}
