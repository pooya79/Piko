package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/web"
)

func (s *Service) Publish(ctx context.Context, botID int64) error {
	snapshot, err := s.LoadDraft(ctx, botID)
	if err != nil {
		return err
	}
	if snapshot.Revision == 0 {
		return ErrNoDraft
	}
	d := snapshot.Definition
	if err := d.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	// Publish only the validated snapshot; a concurrent edit must be retried.
	_, err = s.repo.q.PublishOwnerDraft(ctx, dbgen.PublishOwnerDraftParams{OwnerID: ownerID, ID: botID, Definition: string(data)})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoDraft
	}
	return err
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := h.service.Publish(r.Context(), b.ID); err != nil {
		var invalid *flow.Invalid
		if errors.Is(err, ErrNoDraft) || errors.As(err, &invalid) {
			web.RenderError(w, r, 422, "publish.error.draft")
			return
		}
		h.draftError(w, r, err)
		return
	}
	http.Redirect(w, r, b.URL(), http.StatusSeeOther)
}
