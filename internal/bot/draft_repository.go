package bot

import (
	"context"
	"database/sql"
	"errors"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

func (r *Repository) loadDraft(ctx context.Context, ownerID, botID int64) (Draft, error) {
	row, err := r.q.GetOwnerDraft(ctx, dbgen.GetOwnerDraftParams{OwnerID: ownerID, BotID: botID})
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, nil
	}
	if err != nil {
		return Draft{}, err
	}
	d, err := flow.Decode(row.Definition)
	return Draft{Definition: d, Revision: row.Revision}, err
}
