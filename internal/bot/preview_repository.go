package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

func (r *Repository) createPreview(ctx context.Context, ownerID, botID int64, p Preview) error {
	definition, err := json.Marshal(p.definition)
	if err != nil {
		return err
	}
	state, err := json.Marshal(p.Conversation)
	if err != nil {
		return err
	}
	rows, err := r.q.CreateOwnerPreview(ctx, dbgen.CreateOwnerPreviewParams{OwnerID: ownerID, BotID: botID, PreviewID: p.ID, Definition: string(definition), Conversation: string(state)})
	if err == nil && rows != 1 {
		return ErrNotFound
	}
	return err
}

func (r *Repository) getPreview(ctx context.Context, ownerID, botID int64, id string) (Preview, error) {
	row, err := r.q.GetOwnerPreview(ctx, dbgen.GetOwnerPreviewParams{OwnerID: ownerID, BotID: botID, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return Preview{}, ErrNotFound
	}
	if err != nil {
		return Preview{}, err
	}
	d, err := flow.Decode(row.Definition)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{ID: row.ID, Revision: row.Revision, definition: d}
	if err := json.Unmarshal([]byte(row.Conversation), &p.Conversation); err != nil {
		return Preview{}, err
	}
	return p, nil
}

func (r *Repository) updatePreview(ctx context.Context, ownerID, botID int64, p Preview) error {
	state, err := json.Marshal(p.Conversation)
	if err != nil {
		return err
	}
	rows, err := r.q.UpdateOwnerPreview(ctx, dbgen.UpdateOwnerPreviewParams{OwnerID: ownerID, BotID: botID, PreviewID: p.ID, Revision: p.Revision, Conversation: string(state)})
	if err == nil && rows != 1 {
		return ErrStalePreview
	}
	return err
}
