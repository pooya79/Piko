package bot

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"modernc.org/sqlite"
)

var ErrConnected = errors.New("Bot already has a Telegram identity")

// DuplicateIdentity exposes a link only when the reserved identity belongs to
// this owner. The zero Bot reveals nothing about another owner's reservation.
type DuplicateIdentity struct{ Existing Bot }

func (e *DuplicateIdentity) Error() string { return ErrExists.Error() }
func (e *DuplicateIdentity) Unwrap() error { return ErrExists }

func (s *Service) ConnectExisting(ctx context.Context, botID int64, token string) (Bot, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return Bot{}, err
	}
	if !b.Unconnected {
		return Bot{}, ErrConnected
	}
	ownerID, _ := owner(ctx)
	token = strings.TrimSpace(token)
	identity, delivery, err := s.telegram.Verify(ctx, token)
	if err != nil {
		return Bot{}, err
	}
	encrypted, err := s.credentials.seal(token, ownerID, identity.ID)
	if err != nil {
		return Bot{}, err
	}
	hasWebhook := int64(0)
	if delivery.HasWebhook {
		hasWebhook = 1
	}
	// One guarded UPDATE preserves all child data and excludes a simultaneous
	// connection/deletion. Telegram I/O never holds a SQLite write transaction.
	n, err := s.repo.q.ConnectOwnerUnconnectedBot(ctx, dbgen.ConnectOwnerUnconnectedBotParams{
		OwnerID: ownerID, ID: botID, TelegramID: sql.NullInt64{Int64: identity.ID, Valid: true},
		Name: identity.Name, Username: identity.Username, EncryptedToken: encrypted,
		HasWebhook: hasWebhook, PendingUpdates: delivery.PendingUpdates, VerifiedAt: s.now().Unix(),
	})
	if err != nil {
		var constraint *sqlite.Error
		if !errors.As(err, &constraint) || constraint.Code() != 2067 {
			return Bot{}, err
		}
		// Global uniqueness also reserves Disconnected identities. Lookup only
		// within this owner; never transfer credentials or merge configurations.
		id, lookupErr := s.repo.q.GetOwnerBotByTelegramID(ctx, dbgen.GetOwnerBotByTelegramIDParams{OwnerID: ownerID, TelegramID: sql.NullInt64{Int64: identity.ID, Valid: true}})
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return Bot{}, &DuplicateIdentity{}
		}
		if lookupErr != nil {
			return Bot{}, lookupErr
		}
		existing, err := s.Get(ctx, id)
		if err != nil {
			return Bot{}, err
		}
		return Bot{}, &DuplicateIdentity{Existing: existing}
	}
	if n != 1 {
		return Bot{}, ErrConnected
	}
	return s.Get(ctx, botID)
}
