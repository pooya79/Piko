package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

func (r *Repository) loadDraft(ctx context.Context, ownerID, botID int64) (flow.Definition, bool, error) {
	data, err := r.q.GetOwnerDraft(ctx, dbgen.GetOwnerDraftParams{OwnerID: ownerID, BotID: botID})
	if errors.Is(err, sql.ErrNoRows) {
		return flow.Definition{}, false, nil
	}
	if err != nil {
		return flow.Definition{}, false, err
	}
	d, err := flow.Decode(data)
	return d, true, err
}

func (r *Repository) saveDraft(ctx context.Context, ownerID, botID int64, d flow.Definition) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	rows, err := r.q.SaveOwnerDraft(ctx, dbgen.SaveOwnerDraftParams{OwnerID: ownerID, ID: botID, Definition: string(data)})
	if err == nil && rows != 1 {
		return ErrNotFound
	}
	return err
}
