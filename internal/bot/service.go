package bot

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
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

// Bot exposes verified identity and the saved delivery observation, never credentials.
type Bot struct {
	ID, TelegramID   int64
	Name, Username   string
	HasWebhook       bool
	PendingUpdates   int64
	VerifiedAt       time.Time
	PublishedVersion int64
	DeliveryState    string
	DeliveryError    bool
	WebhookIsPiko    bool
}

func (b Bot) URL() string { return "/bots/" + strconv.FormatInt(b.ID, 10) }

type Service struct {
	repo        *Repository
	telegram    *telegram.Client
	credentials credentials
	publicURL   string
}

func NewService(repo *Repository, api *telegram.Client, key []byte) (*Service, error) {
	c, err := newCredentials(key)
	if err != nil {
		return nil, err
	}
	return &Service{repo: repo, telegram: api, credentials: c}, nil
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
	return s.repo.list(ctx, id)
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
	return b, err
}
func Navigation(bots []Bot) shell.Page {
	p := shell.Page{BotsURL: "/bots", ConnectBotURL: "/bots/connect"}
	for i, b := range bots {
		if i == 5 {
			break
		}
		p.RecentBots = append(p.RecentBots, shell.BotLink{Name: b.Name, URL: b.URL()})
	}
	return p
}
