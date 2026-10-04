package builder

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/firebase/genkit/go/genkit"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/platform/database"
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

func (m Message) FeedbackKey() string {
	if m.Role == ResultRole {
		for _, status := range []RunStatus{RunFailed, RunTimeout, RunInterrupted} {
			if m.Content == status.LocaleKey() {
				return m.Content
			}
		}
	}
	return ""
}

// Conversation is the history boundary for the model-response slice. It contains
// this chat's full ordered history; the current shared Draft comes from bot.LoadDraft.
type Conversation struct {
	Chat     Chat
	Messages []Message
	Runs     []Run
}

func (c Conversation) LatestRun() Run {
	if len(c.Runs) == 0 {
		return Run{}
	}
	return c.Runs[len(c.Runs)-1]
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
	repo           *Repository
	bots           *bot.Service
	config         Config
	genkit         *genkit.Genkit
	now            func() time.Time
	mu             sync.Mutex
	commitMu       sync.Mutex
	stopping       bool
	work           context.Context
	cancel         context.CancelFunc
	runs           sync.WaitGroup
	storageContext context.Context
	cancelStorage  context.CancelFunc
	storageTimer   *time.Timer
}

func NewService(repo *Repository, bots *bot.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	storageContext, cancelStorage := context.WithCancel(context.Background())
	return &Service{repo: repo, bots: bots, now: time.Now, config: (Config{}).defaults(), work: ctx, cancel: cancel, storageContext: storageContext, cancelStorage: cancelStorage}
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
	q := s.repo.q.WithTx(tx)
	if err := recoverRuns(ctx, q); err != nil {
		return Conversation{}, err
	}
	history, err := loadHistory(ctx, q, ownerID, botID, chatID)
	if err != nil {
		return Conversation{}, err
	}
	history.Runs, err = loadRuns(ctx, q, ownerID, botID, chatID)
	if err != nil {
		return Conversation{}, err
	}
	return history, tx.Commit()
}

// Status avoids loading full history or per-call accounting on every display poll.
func (s *Service) Status(ctx context.Context, botID, chatID int64) (Run, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Run{}, err
	}
	var run Run
	err = database.RetryWrite(ctx, s.repo.db, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		q := s.repo.q.WithTx(tx)
		if _, err := q.GetOwnerBuilderChat(ctx, dbgen.GetOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, ChatID: chatID}); err != nil {
			return storageError(err)
		}
		if err := recoverRuns(ctx, q); err != nil {
			return err
		}
		row, err := q.GetLatestOwnerBuilderRunStatus(ctx, dbgen.GetLatestOwnerBuilderRunStatusParams{OwnerID: ownerID, BotID: botID, ChatID: sql.NullInt64{Int64: chatID, Valid: true}})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		run = Run{ID: row.ID, Status: RunStatus(row.Status)}
		return tx.Commit()
	})
	return run, err
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

// Deletion and admission share the immediate write transaction. Accounting is
// retained; active history stays available until cancellation-aware deletion.
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
	q := s.repo.q.WithTx(tx)
	if _, err := q.GetOwnerBuilderChat(ctx, dbgen.GetOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, ChatID: chatID}); err != nil {
		return storageError(err)
	}
	if err := recoverRuns(ctx, q); err != nil {
		return err
	}
	active, err := q.ActiveOwnerBuilderChat(ctx, dbgen.ActiveOwnerBuilderChatParams{OwnerID: ownerID, ChatID: sql.NullInt64{Int64: chatID, Valid: true}})
	if err != nil {
		return err
	}
	if active > 0 {
		return ErrBusy
	}
	n, err := q.DeleteOwnerBuilderChat(ctx, dbgen.DeleteOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, ChatID: chatID})
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
