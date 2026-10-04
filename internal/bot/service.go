package bot

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/web/shell"
)

var (
	ErrExists       = errors.New("Bot identity already connected")
	ErrNotFound     = errors.New("Bot not found")
	ErrUnauthorized = errors.New("Bot owner required")
)

type DeliveryMode string

const (
	PollingDelivery DeliveryMode = "polling"
	WebhookDelivery DeliveryMode = "webhook"
)

// Bot exposes workspace identity and saved Telegram state, never credentials.
// TelegramID is meaningful only when Unconnected is false.
type Bot struct {
	ID, TelegramID   int64
	Name, Username   string
	HasWebhook       bool
	PendingUpdates   int64
	VerifiedAt       time.Time
	PublishedVersion int64
	DeliveryState    string
	DeliveryMode     DeliveryMode
	DeliveryError    bool
	WebhookIsPiko    bool
	Paused           bool
	Disconnected     bool
	Unconnected      bool
}

func (b Bot) URL() string { return "/bots/" + strconv.FormatInt(b.ID, 10) }

type Service struct {
	repo        *Repository
	telegram    *telegram.Client
	credentials credentials
	publicURL   string
	now         func() time.Time
	workerMu    sync.Mutex
	workers     map[int64]context.CancelFunc
}

func NewService(repo *Repository, api *telegram.Client, key []byte, now func() time.Time) (*Service, error) {
	c, err := newCredentials(key)
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, telegram: api, credentials: c, now: now, workers: make(map[int64]context.CancelFunc)}, nil
}
func owner(ctx context.Context) (int64, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok || u.ID <= 0 {
		return 0, ErrUnauthorized
	}
	return u.ID, nil
}
func (s *Service) Connect(ctx context.Context, token string) (Bot, error) {
	id, err := owner(ctx)
	if err != nil {
		return Bot{}, err
	}
	token = strings.TrimSpace(token)
	identity, delivery, err := s.telegram.Verify(ctx, token)
	if err != nil {
		return Bot{}, err
	}
	encrypted, err := s.credentials.seal(token, id, identity.ID)
	if err != nil {
		return Bot{}, err
	}
	// Telegram I/O finishes before the single atomic INSERT acquires a write lock.
	return s.repo.create(ctx, id, identity, delivery, encrypted)
}
func (s *Service) List(ctx context.Context) ([]Bot, error) {
	id, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	bots, err := s.repo.list(ctx, id)
	for i := range bots {
		bots[i] = s.deliveryStatus(bots[i])
	}
	return bots, err
}
func (s *Service) Get(ctx context.Context, id int64) (Bot, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Bot{}, err
	}
	b, err := s.repo.get(ctx, ownerID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Bot{}, ErrNotFound
	}
	return s.deliveryStatus(b), err
}

// Activation survives restarts, but changing the receiver mode requires the
// owner to activate again. Keep the stored mode visible without claiming work
// that this server's workers cannot receive.
func (s *Service) deliveryStatus(b Bot) Bot {
	if b.Unconnected {
		b.DeliveryState = "unconnected"
		b.DeliveryError = false
		return b
	}
	if b.Disconnected {
		b.DeliveryState = "disconnected"
		b.DeliveryError = false
		return b
	}
	if b.DeliveryState == "active" && b.DeliveryMode != s.deliveryMode() {
		b.DeliveryState = "mode.changed"
	}
	if b.DeliveryState == "active" && b.Paused {
		b.DeliveryState = "paused"
	}
	return b
}
func Navigation(bots []Bot) shell.Page {
	p := shell.Page{BotsURL: "/bots", CreateBotURL: "/bots/new", BuilderURL: "/builder"}
	for i, b := range bots {
		if i == 5 {
			break
		}
		p.RecentBots = append(p.RecentBots, shell.BotLink{Name: b.Name, URL: b.URL()})
	}
	return p
}
