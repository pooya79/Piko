package bot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var (
	ErrUnconnected  = errors.New("Bot has no Telegram identity")
	ErrDisconnected = errors.New("Bot credentials removed")
	ErrIdentity     = errors.New("replacement identifies another Telegram Bot")
)

// ReplaceToken verifies before stopping anything. Reconnection uses the same
// identity check; both leave activation as a separate owner decision.
func (s *Service) ReplaceToken(ctx context.Context, botID int64, token string, reconnect bool) (bool, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return false, err
	}
	if b.Unconnected {
		return false, ErrUnconnected
	}
	if b.Disconnected != reconnect {
		return false, ErrDisconnected
	}
	token = strings.TrimSpace(token)
	identity, delivery, err := s.telegram.Verify(ctx, token)
	if err != nil {
		return false, err
	}
	if identity.ID != b.TelegramID {
		return false, ErrIdentity
	}
	ownerID, _ := owner(ctx)
	encrypted, err := s.credentials.seal(token, ownerID, b.TelegramID)
	if err != nil {
		return false, err
	}
	disconnected := int64(0)
	if reconnect {
		disconnected = 1
	}
	claim, err := s.stopOwnerDelivery(ctx, ownerID, b.ID, disconnected)
	if err != nil {
		return false, err
	}
	defer s.releaseLifecycle(ctx, ownerID, b.ID, claim)
	b, err = s.Get(ctx, botID)
	if err != nil {
		return false, err
	}
	warning := false
	if !reconnect {
		delivery, warning = s.cleanRemoteDelivery(ctx, b, token, delivery)
	}
	return warning, s.saveLifecycle(ctx, ownerID, b, claim, encrypted, identity, delivery, false)
}

func (s *Service) Disconnect(ctx context.Context, botID int64, deleteData bool) (bool, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return false, err
	}
	ownerID, _ := owner(ctx)
	if b.Unconnected {
		if !deleteData {
			return false, ErrUnconnected
		}
		// There can be no Telegram work to stop. The identity guard excludes a
		// concurrent future connection without manufacturing delivery state.
		n, err := s.repo.q.DeleteOwnerUnconnectedBot(ctx, dbgen.DeleteOwnerUnconnectedBotParams{OwnerID: ownerID, ID: botID})
		if err != nil {
			return false, err
		}
		if n != 1 {
			return false, ErrActivationBusy
		}
		return false, nil
	}
	claim, err := s.stopOwnerDelivery(ctx, ownerID, botID, -1)
	if err != nil {
		return false, err
	}
	defer s.releaseLifecycle(ctx, ownerID, botID, claim)
	b, err = s.Get(ctx, botID)
	if err != nil {
		return false, err
	}
	_, token, tokenErr := s.ownerToken(ctx, botID)
	delivery := telegram.Delivery{HasWebhook: b.HasWebhook, PendingUpdates: b.PendingUpdates}
	warning := !b.Disconnected && tokenErr != nil
	if !b.Disconnected && tokenErr == nil {
		delivery, warning = s.cleanRemoteDelivery(ctx, b, token, delivery)
	}
	return warning, s.saveLifecycle(ctx, ownerID, b, claim, []byte{}, telegram.Identity{Name: b.Name, Username: b.Username}, delivery, deleteData)
}

// The persisted activation claim also excludes lifecycle mutations across
// server processes. Stop intake first, cancel local work, then join via its
// lease. Network calls never run inside a SQLite transaction.
func (s *Service) stopOwnerDelivery(ctx context.Context, ownerID, botID, disconnected int64) (string, error) {
	secret, err := nonce()
	if err != nil {
		return "", err
	}
	b, err := s.Get(ctx, botID)
	if err != nil {
		return "", err
	}
	encrypted, err := s.credentials.seal(secret, ownerID, -b.TelegramID)
	if err != nil {
		return "", err
	}
	if _, err = s.repo.q.EnsureOwnerDelivery(ctx, dbgen.EnsureOwnerDeliveryParams{OwnerID: ownerID, ID: botID, EncryptedSecret: encrypted}); err != nil {
		return "", err
	}
	claim, err := nonce()
	if err != nil {
		return "", err
	}
	s.workerMu.Lock()
	n, err := s.repo.q.BeginOwnerLifecycle(ctx, dbgen.BeginOwnerLifecycleParams{OwnerID: ownerID, BotID: botID, ActivationNonce: claim, ExpectedDisconnected: disconnected})
	if err == nil && n == 1 {
		if cancel := s.workers[botID]; cancel != nil {
			cancel()
		}
	}
	s.workerMu.Unlock()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrActivationBusy
	}
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		d, e := s.repo.q.GetBotDelivery(waitCtx, botID)
		if e != nil {
			s.releaseLifecycle(ctx, ownerID, botID, claim)
			return "", e
		}
		if d.WorkerUntil < time.Now().Unix() {
			return claim, nil
		}
		select {
		case <-waitCtx.Done():
			s.releaseLifecycle(ctx, ownerID, botID, claim)
			return "", ErrActivationBusy
		case <-ticker.C:
		}
	}
}

func (s *Service) releaseLifecycle(ctx context.Context, ownerID, botID int64, claim string) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverySaveTimeout)
	defer cancel()
	_, _ = s.repo.q.FinishOwnerActivation(finishCtx, dbgen.FinishOwnerActivationParams{OwnerID: ownerID, BotID: botID, State: "inactive", ActivationNonce: claim})
}

// Telegram has no conditional deleteWebhook operation. Inspect immediately
// before deletion and leave every different destination untouched. No polling
// cleanup is necessary after its local worker has stopped.
func (s *Service) cleanRemoteDelivery(ctx context.Context, b Bot, token string, previous telegram.Delivery) (telegram.Delivery, bool) {
	if token == "" {
		return previous, true
	}
	observed, err := s.telegram.Inspect(ctx, token)
	if err != nil {
		return previous, true
	}
	if observed.URL == "" || !b.WebhookIsPiko || s.publicURL == "" || observed.URL != s.endpoint(b.ID) {
		return observed, false
	}
	if err := s.telegram.DeleteWebhook(ctx, token); err != nil {
		return observed, true
	}
	observed.HasWebhook = false
	observed.URL = ""
	return observed, false
}

func (s *Service) saveLifecycle(ctx context.Context, ownerID int64, b Bot, claim string, encrypted []byte, identity telegram.Identity, observed telegram.Delivery, deleteData bool) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverySaveTimeout)
	defer cancel()
	tx, err := s.repo.db.BeginTx(finishCtx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	secret, err := nonce()
	if err != nil {
		return err
	}
	sealed, err := s.credentials.seal(secret, ownerID, -b.TelegramID)
	if err != nil {
		return err
	}
	if len(encrypted) == 0 {
		sealed = []byte{}
	}
	n, err := q.FinishOwnerLifecycle(finishCtx, dbgen.FinishOwnerLifecycleParams{OwnerID: ownerID, BotID: b.ID, ActivationNonce: claim, EncryptedSecret: sealed})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrActivationBusy
	}
	if deleteData {
		_, err = q.DeleteOwnerBot(finishCtx, dbgen.DeleteOwnerBotParams{OwnerID: ownerID, ID: b.ID})
	} else {
		has, isPiko := int64(0), int64(0)
		if observed.HasWebhook {
			has = 1
		}
		if observed.URL != "" && s.publicURL != "" && observed.URL == s.endpoint(b.ID) {
			isPiko = 1
		}
		_, err = q.SetOwnerCredentials(finishCtx, dbgen.SetOwnerCredentialsParams{OwnerID: ownerID, ID: b.ID, EncryptedToken: encrypted, Name: identity.Name, Username: identity.Username, HasWebhook: has, PendingUpdates: observed.PendingUpdates, WebhookIsPiko: isPiko})
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func credentialStatus(b Bot) string {
	if b.Unconnected {
		return "bot.status.unconnected"
	}
	if b.Disconnected {
		return "lifecycle.credentials.removed"
	}
	return "bot.status.verified"
}
