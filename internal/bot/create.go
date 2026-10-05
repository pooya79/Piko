package bot

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrBotName = errors.New("Bot name must contain 1 to 80 characters")

// ValidateName applies the same workspace-name rules to manual and staged creation.
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 80 || strings.ContainsFunc(name, unicode.IsControl) {
		return ErrBotName
	}
	return nil
}

// CreateDraftTx validates and creates an initial Bot inside the caller's
// transaction, allowing chat association and the successful reply to commit together.
func (s *Service) CreateDraftTx(ctx context.Context, tx *sql.Tx, name string, d flow.Definition) (int64, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return 0, err
	}
	name = strings.TrimSpace(name)
	if ValidateName(name) != nil {
		return 0, ErrBotName
	}
	if err := d.Validate(); err != nil {
		return 0, err
	}
	q := s.repo.q.WithTx(tx)
	id, err := q.CreateUnconnectedBot(ctx, dbgen.CreateUnconnectedBotParams{OwnerID: ownerID, Name: name})
	if err != nil {
		return 0, err
	}
	_, err = saveDraft(ctx, q, ownerID, id, 0, d)
	return id, err
}

func (s *Service) Rename(ctx context.Context, botID int64, name string) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if ValidateName(name) != nil {
		return ErrBotName
	}
	n, err := s.repo.q.RenameOwnerBot(ctx, dbgen.RenameOwnerBotParams{OwnerID: ownerID, BotID: botID, Name: name})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// Create saves an owner-owned Unconnected Bot and its usable first Draft
// together. No Telegram identity or credential is required or fabricated.
func (s *Service) Create(ctx context.Context, name string) (Bot, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Bot{}, err
	}
	name = strings.TrimSpace(name)
	if ValidateName(name) != nil {
		return Bot{}, ErrBotName
	}
	d := emptyDraft()
	d.Menu.Choices = []flow.Choice{{ID: "about", Label: "دربارهٔ ربات", Target: "about-message"}}
	d.Messages = []flow.Block{{ID: "about-message", Type: "message", Text: "به ربات من خوش آمدید!"}}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return Bot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.repo.q.WithTx(tx)
	id, err := s.CreateDraftTx(ctx, tx, name, d)
	if err != nil {
		return Bot{}, err
	}
	row, err := q.GetOwnerBot(ctx, dbgen.GetOwnerBotParams{OwnerID: ownerID, ID: id})
	if err != nil {
		return Bot{}, err
	}
	if err := tx.Commit(); err != nil {
		return Bot{}, err
	}
	return s.deliveryStatus(botFromRow(row)), nil
}
