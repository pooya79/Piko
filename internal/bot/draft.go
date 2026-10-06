package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

func emptyDraft() flow.Definition {
	return flow.Definition{Version: 1, Welcome: flow.Block{ID: "welcome", Type: "message", Text: "سلام! از منو شروع کنید."}, Menu: flow.Block{ID: "menu", Type: "menu", Text: "چه کاری می\u200cخواهید انجام دهید؟"}}
}

// Draft is a validated saved snapshot. Revision zero means no Draft has been saved.
type Draft struct {
	Definition flow.Definition
	Revision   int64
}

var ErrStaleDraft = errors.New("Draft has changed since it was loaded")

func (s *Service) LoadDraft(ctx context.Context, botID int64) (Draft, error) {
	if _, err := s.Get(ctx, botID); err != nil {
		return Draft{}, err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return Draft{}, err
	}
	return s.repo.loadDraft(ctx, ownerID, botID)
}

// SaveDraftTx applies a validated candidate with Builder's atomic run outcome.
// The caller owns an immediate transaction on the application's SQLite file.
func (s *Service) SaveDraftTx(ctx context.Context, tx *sql.Tx, botID, expectedRevision int64, d flow.Definition) (int64, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return 0, err
	}
	return saveDraft(ctx, s.repo.q.WithTx(tx), ownerID, botID, expectedRevision, d)
}

// Creation and subsequent edits share the same ownership, validation and
// revision guard inside their caller's immediate transaction.
func saveDraft(ctx context.Context, q *dbgen.Queries, ownerID, botID, expectedRevision int64, d flow.Definition) (int64, error) {
	// The configured immediate transaction serializes ownership and first saves.
	if _, err := q.GetOwnerBot(ctx, dbgen.GetOwnerBotParams{OwnerID: ownerID, ID: botID}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if err := d.Validate(); err != nil {
		return 0, err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return 0, err
	}
	revision, err := q.SaveOwnerDraft(ctx, dbgen.SaveOwnerDraftParams{OwnerID: ownerID, BotID: botID, Definition: string(data), ExpectedRevision: expectedRevision})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrStaleDraft
	}
	if err != nil {
		return 0, err
	}
	return revision, nil
}
