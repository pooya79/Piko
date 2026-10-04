package bot

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrInboxFull = errors.New("delivery inbox is full")

func (s *Service) accept(ctx context.Context, botID int64, data []byte, u telegram.Update) error {
	if u.ID == nil {
		return errors.New("invalid update")
	}
	return s.acceptQueries(ctx, s.repo.q, botID, data, u)
}

func (s *Service) acceptQueries(ctx context.Context, q *dbgen.Queries, botID int64, data []byte, u telegram.Update) error {
	exists, err := q.HasAcceptedUpdate(ctx, dbgen.HasAcceptedUpdateParams{BotID: botID, UpdateID: *u.ID})
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	participantID := int64(0)
	if u.Message != nil {
		participantID = u.Message.From.ID
	}
	if u.Callback != nil {
		participantID = u.Callback.From.ID
	}
	n, err := q.AcceptUpdate(ctx, dbgen.AcceptUpdateParams{BotID: botID, UpdateID: *u.ID, Payload: string(data), ParticipantID: participantID})
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	// A concurrent duplicate remains successful even when the queue is now full.
	exists, err = q.HasAcceptedUpdate(ctx, dbgen.HasAcceptedUpdateParams{BotID: botID, UpdateID: *u.ID})
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ErrInboxFull
}

func (s *Service) authenticateDelivery(ctx context.Context, id int64, secret string) error {
	return s.authenticateDeliveryQueries(ctx, s.repo.q, id, secret)
}

func (s *Service) authenticateDeliveryQueries(ctx context.Context, q *dbgen.Queries, id int64, secret string) error {
	row, err := q.GetIngressCredentials(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	expected, err := s.credentials.open(row.EncryptedSecret, row.OwnerID, -row.TelegramID)
	if err != nil || subtle.ConstantTimeCompare([]byte(expected), []byte(secret)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	secret := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if err != nil || id <= 0 || secret == "" || len(secret) > 256 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := h.service.authenticateDelivery(r.Context(), id, secret); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "delivery unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, (256<<10)+1))
	if err != nil || len(data) > 256<<10 {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	u, err := telegram.DecodeUpdate(data)
	if err != nil {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	if err = h.service.receiveWebhook(r.Context(), id, secret, data, u); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "delivery unavailable", http.StatusServiceUnavailable)
		return
	}
	// Acknowledge durable receipt only. Runtime execution happens independently.
	w.WriteHeader(http.StatusOK)
}

// Recheck the secret and accept in one immediate transaction. An HTTP request
// authenticated before disconnect cannot cross a reconnect/activation boundary.
func (s *Service) receiveWebhook(ctx context.Context, id int64, secret string, data []byte, u telegram.Update) error {
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	if err = s.authenticateDeliveryQueries(ctx, q, id, secret); err != nil {
		return err
	}
	if err = s.acceptQueries(ctx, q, id, data, u); err != nil {
		return err
	}
	return tx.Commit()
}
