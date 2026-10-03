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
	exists, err := s.repo.q.HasAcceptedUpdate(ctx, dbgen.HasAcceptedUpdateParams{BotID: botID, UpdateID: *u.ID})
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	participantID:=int64(0);if u.Message!=nil {participantID=u.Message.From.ID};if u.Callback!=nil{participantID=u.Callback.From.ID}
n, err := s.repo.q.AcceptUpdate(ctx, dbgen.AcceptUpdateParams{BotID: botID, UpdateID: *u.ID, Payload: string(data),ParticipantID:participantID})
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	// A concurrent duplicate remains successful even when the queue is now full.
	exists, err = s.repo.q.HasAcceptedUpdate(ctx, dbgen.HasAcceptedUpdateParams{BotID: botID, UpdateID: *u.ID})
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ErrInboxFull
}

func (s *Service) authenticateDelivery(ctx context.Context,id int64,secret string) error {
 row,err:=s.repo.q.GetIngressCredentials(ctx,id)
 if errors.Is(err,sql.ErrNoRows){return ErrUnauthorized};if err!=nil{return err}
 expected,err:=s.credentials.open(row.EncryptedSecret,row.OwnerID,-row.TelegramID)
 if err!=nil || subtle.ConstantTimeCompare([]byte(expected),[]byte(secret))!=1{return ErrUnauthorized};return nil
}

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	secret := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if err != nil || id <= 0 || secret == "" || len(secret) > 256 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
 if err:=h.service.authenticateDelivery(r.Context(),id,secret);err!=nil {
  if errors.Is(err,ErrUnauthorized){http.Error(w,"unauthorized",http.StatusUnauthorized)}else{http.Error(w,"delivery unavailable",http.StatusServiceUnavailable)};return
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
	if err = h.service.accept(r.Context(), id, data, u); err != nil {
		http.Error(w, "delivery unavailable", http.StatusServiceUnavailable)
		return
	}
	// Acknowledge durable receipt only. Runtime execution happens independently.
	w.WriteHeader(http.StatusOK)
}
