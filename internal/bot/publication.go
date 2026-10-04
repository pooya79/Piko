package bot

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/web"
)

func (s *Service) Publish(ctx context.Context, botID int64) error {
	_, err := s.publish(ctx, botID, false)
	return err
}

func (s *Service) publish(ctx context.Context, botID int64, requireCredentials bool) (int64, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return 0, err
	}
	var version int64
	err = database.RetryWrite(ctx, s.repo.db, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		q := dbgen.New(tx)
		b, err := q.GetOwnerBot(ctx, dbgen.GetOwnerBotParams{OwnerID: ownerID, ID: botID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// The immediate transaction serializes with Builder admission and completion,
		// manual saves and Undo, across server processes. No upstream I/O runs here.
		active, err := q.ActiveOwnerBuilderBot(ctx, dbgen.ActiveOwnerBuilderBotParams{OwnerID: ownerID, BotID: sql.NullInt64{Int64: botID, Valid: true}})
		if err != nil {
			return err
		}
		if active > 0 {
			return ErrBuilderBusy
		}
		if requireCredentials {
			if !b.TelegramID.Valid {
				return ErrUnconnected
			}
			credentials, err := q.GetOwnerBotCredentials(ctx, dbgen.GetOwnerBotCredentialsParams{OwnerID: ownerID, ID: botID})
			if err != nil {
				return err
			}
			if len(credentials.EncryptedToken) == 0 {
				return ErrDisconnected
			}
		}
		draft, err := q.GetOwnerDraft(ctx, dbgen.GetOwnerDraftParams{OwnerID: ownerID, BotID: botID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoDraft
		}
		if err != nil {
			return err
		}
		if _, err := flow.Decode(draft.Definition); err != nil {
			return err
		}
		version, err = q.PublishOwnerDraft(ctx, dbgen.PublishOwnerDraftParams{OwnerID: ownerID, ID: botID, Definition: draft.Definition})
		if err != nil {
			return err
		}
		return tx.Commit()
	})
	return version, err
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := h.service.Publish(r.Context(), b.ID); err != nil {
		if errors.Is(err, ErrBuilderBusy) {
			web.RenderError(w, r, 409, "deploy.busy")
			return
		}
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
