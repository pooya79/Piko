package builder

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrTitle = errors.New("builder chat title must contain 1–80 characters")
var ErrMessage = errors.New("invalid Builder history message")

type Role string

const (
	OwnerRole  Role = "owner"
	ModelRole  Role = "model"
	ResultRole Role = "result"
)

type Message struct {
	Sequence  int64
	Role      Role
	Content   string
	CreatedAt time.Time
}

// Conversation is the history boundary for the model-response slice. It contains
// this chat's full ordered history; the current shared Draft comes from bot.LoadDraft.
type Conversation struct {
	Chat     Chat
	Messages []Message
}

type Chat struct {
	ID, BotID            int64
	Title                string
	CreatedAt, UpdatedAt time.Time
}

func (c Chat) URL() string {
	return "/bots/" + strconv.FormatInt(c.BotID, 10) + "/chats/" + strconv.FormatInt(c.ID, 10)
}

type Service struct {
	repo *Repository
	bots *bot.Service
}

func NewService(repo *Repository, bots *bot.Service) *Service {
	return &Service{repo: repo, bots: bots}
}

func owner(ctx context.Context) (int64, error) {
	u, ok := auth.UserFromContext(ctx)
	if !ok || u.ID <= 0 {
		return 0, bot.ErrUnauthorized
	}
	return u.ID, nil
}

func (s *Service) Create(ctx context.Context, botID int64, title string) (Chat, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Chat{}, err
	}
	if _, err := s.bots.Get(ctx, botID); err != nil {
		return Chat{}, err
	}
	title = strings.TrimSpace(title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) < 1 || utf8.RuneCountInString(title) > 80 {
		return Chat{}, ErrTitle
	}
	return s.repo.create(ctx, ownerID, botID, title)
}

func (s *Service) List(ctx context.Context, botID int64) ([]Chat, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.bots.Get(ctx, botID); err != nil {
		return nil, err
	}
	return s.repo.list(ctx, ownerID, botID)
}

func (s *Service) History(ctx context.Context, botID, chatID int64) (Conversation, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Conversation{}, err
	}
	// Keep chat metadata and full history in one snapshot during deletion/appends.
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	history, err := loadHistory(ctx, s.repo.q.WithTx(tx), ownerID, botID, chatID)
	if err != nil {
		return Conversation{}, err
	}
	return history, tx.Commit()
}

// Append saves one real owner/model/result entry, never a fabricated reply.
// The runtime can use the transaction helper to commit results with run state.
func (s *Service) Append(ctx context.Context, botID, chatID int64, role Role, content string) (Message, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Message{}, err
	}
	if role != OwnerRole && role != ModelRole && role != ResultRole {
		return Message{}, ErrMessage
	}
	if !utf8.ValidString(content) || strings.TrimSpace(content) == "" || utf8.RuneCountInString(content) > 32768 {
		return Message{}, ErrMessage
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback() }()
	message, err := appendMessage(ctx, s.repo.q.WithTx(tx), ownerID, botID, chatID, role, content)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// All chats are idle until the run-runtime slice adds admission and cancellation.
// That slice must coordinate active-run deletion in this same write transaction.
func (s *Service) Delete(ctx context.Context, botID, chatID int64) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := s.repo.q.WithTx(tx).DeleteOwnerBuilderChat(ctx, dbgen.DeleteOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, ChatID: chatID})
	if err != nil {
		return err
	}
	if n != 1 {
		return bot.ErrNotFound
	}
	return tx.Commit()
}

func storageError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return bot.ErrNotFound
	}
	return err
}
