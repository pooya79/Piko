package bot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"time"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

const deliverySaveTimeout = 3 * time.Second

var ErrActivationBusy = errors.New("activation in progress")

type Activation struct {
	Bot      Bot
	Conflict string
	Pending  int64
	Polling  bool
}

func ValidateDeliveryConfig(environment, endpoint string) error {
	if endpoint == "" {
		if environment == "production" {
			return errors.New("BOT_PUBLIC_URL is required in production")
		}
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443" && u.Port() != "80" && u.Port() != "88" && u.Port() != "8443") {
		return errors.New("BOT_PUBLIC_URL must be a public HTTPS origin without credentials, path, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || (ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback())) {
		return errors.New("BOT_PUBLIC_URL must use a public host")
	}
	return nil
}
func nonce() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (s *Service) ConfigureDelivery(environment, endpoint string) error {
	if err := ValidateDeliveryConfig(environment, endpoint); err != nil {
		return err
	}
	s.publicURL = strings.TrimRight(endpoint, "/")
	return nil
}
func (s *Service) endpoint(botID int64) string {
	return s.publicURL + "/telegram/bots/" + strconv.FormatInt(botID, 10)
}

func (s *Service) deliveryMode() DeliveryMode {
	if s.publicURL == "" {
		return PollingDelivery
	}
	return WebhookDelivery
}

func (s *Service) ownerToken(ctx context.Context, botID int64) (dbgen.GetOwnerBotCredentialsRow, string, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return dbgen.GetOwnerBotCredentialsRow{}, "", err
	}
	row, err := s.repo.q.GetOwnerBotCredentials(ctx, dbgen.GetOwnerBotCredentialsParams{OwnerID: ownerID, ID: botID})
	if err != nil {
		return row, "", err
	}
	token, err := s.credentials.open(row.EncryptedToken, row.OwnerID, row.TelegramID)
	return row, token, err
}
func (s *Service) InspectActivation(ctx context.Context, botID int64) (Activation, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return Activation{}, err
	}
	if b.Disconnected {
		return Activation{}, ErrDisconnected
	}
	if b.PublishedVersion == 0 {
		return Activation{}, ErrNoDraft
	}
	_, token, err := s.ownerToken(ctx, botID)
	if err != nil {
		return Activation{}, err
	}
	observed, err := s.telegram.Inspect(ctx, token)
	if err != nil {
		return Activation{}, err
	}
	a := Activation{Bot: b, Pending: observed.PendingUpdates, Polling: s.publicURL == ""}
	if observed.URL != "" && observed.URL != s.endpoint(botID) {
		hash := sha256.Sum256([]byte(observed.URL))
		a.Conflict = base64.RawURLEncoding.EncodeToString(hash[:])
	}
	return a, nil
}
func (s *Service) Activate(ctx context.Context, botID int64, confirmation string) (Activation, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return Activation{}, err
	}
	if b.Disconnected {
		return Activation{}, ErrDisconnected
	}
	if b.PublishedVersion == 0 {
		return Activation{}, ErrNoDraft
	}
	row, token, err := s.ownerToken(ctx, botID)
	if err != nil {
		return Activation{}, err
	}
	a := Activation{Bot: b}
	secret, err := nonce()
	if err != nil {
		return a, err
	}
	// Domain separation prevents swapping encrypted credentials and webhook secrets.
	encrypted, err := s.credentials.seal(secret, row.OwnerID, -row.TelegramID)
	if err != nil {
		return a, err
	}
	if _, err = s.repo.q.EnsureOwnerDelivery(ctx, dbgen.EnsureOwnerDeliveryParams{OwnerID: row.OwnerID, ID: botID, EncryptedSecret: encrypted}); err != nil {
		return a, err
	}
	delivery, err := s.repo.q.GetBotDelivery(ctx, botID)
	if err != nil {
		return a, err
	}
	secret, err = s.credentials.open(delivery.EncryptedSecret, row.OwnerID, -row.TelegramID)
	if err != nil {
		return a, err
	}
	// Capture credentials before the fresh inspection, then compare their
	// encrypted snapshots atomically when claiming activation. A lifecycle change
	// during either network/SQL reads makes this activation stale.
	a, err = s.InspectActivation(ctx, botID)
	if err != nil {
		return a, err
	}
	if a.Conflict != "" && a.Conflict != confirmation {
		return a, ErrWebhookConflict
	}
	claim, err := nonce()
	if err != nil {
		return a, err
	}
	n, err := s.repo.q.BeginOwnerActivation(ctx, dbgen.BeginOwnerActivationParams{OwnerID: row.OwnerID, BotID: botID, Mode: string(s.deliveryMode()), ActivationNonce: claim, ExpectedToken: row.EncryptedToken, ExpectedSecret: delivery.EncryptedSecret})
	if err != nil {
		return a, err
	}
	if n != 1 {
		return a, ErrActivationBusy
	}
	if a.Polling {
		err = s.telegram.DeleteWebhook(ctx, token)
	} else {
		err = s.telegram.SetWebhook(ctx, token, s.endpoint(botID), secret)
	}
	state := "active"
	if err != nil {
		state = "error"
	}
	// Persist uncertain results even when the browser request has disconnected.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverySaveTimeout)
	defer cancel()
	tx, saveErr := s.repo.db.BeginTx(finishCtx, nil)
	if saveErr != nil {
		return a, saveErr
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	n, saveErr = q.FinishOwnerActivation(finishCtx, dbgen.FinishOwnerActivationParams{OwnerID: row.OwnerID, BotID: botID, State: state, ActivationNonce: claim})
	if saveErr != nil {
		return a, saveErr
	}
	if n != 1 {
		return a, ErrActivationBusy
	}
	if err == nil {
		hasWebhook := int64(1)
		if a.Polling {
			hasWebhook = 0
		}
		if saveErr := q.ObserveOwnerDelivery(finishCtx, dbgen.ObserveOwnerDeliveryParams{OwnerID: row.OwnerID, ID: botID, HasWebhook: hasWebhook, PendingUpdates: a.Pending, WebhookIsPiko: hasWebhook}); saveErr != nil {
			return a, saveErr
		}
	}
	if saveErr := tx.Commit(); saveErr != nil {
		return a, saveErr
	}
	return a, err
}

var ErrWebhookConflict = errors.New("webhook conflict confirmation required")
