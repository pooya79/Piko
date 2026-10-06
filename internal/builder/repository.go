package builder

import (
	"context"
	"database/sql"
	"time"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

type Repository struct {
	db *sql.DB
	q  *dbgen.Queries
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db, q: dbgen.New(db)}
}

func chatFromRow(row dbgen.BuilderChat) Chat {
	return Chat{ID: row.ID, BotID: row.BotID.Int64, Title: row.Title, CreatedAt: time.Unix(row.CreatedAt, 0), UpdatedAt: time.Unix(row.UpdatedAt, 0)}
}

func (r *Repository) create(ctx context.Context, ownerID, botID int64, title string) (Chat, error) {
	if botID == 0 {
		row, err := r.q.CreateOwnerPikoChat(ctx, dbgen.CreateOwnerPikoChatParams{OwnerID: ownerID, Title: title, CreatedAt: time.Now().Unix()})
		return chatFromRow(row), storageError(err)
	}
	row, err := r.q.CreateOwnerBuilderChat(ctx, dbgen.CreateOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, Title: title, CreatedAt: time.Now().Unix()})
	return chatFromRow(row), storageError(err)
}

func (r *Repository) savedChats(ctx context.Context, ownerID int64) ([]Chat, error) {
	rows, err := r.q.ListOwnerPikoChats(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	chats := make([]Chat, 0, len(rows))
	for _, row := range rows {
		chats = append(chats, Chat{ID: row.ID, BotID: row.BotID.Int64, Title: row.Title, BotName: row.BotName, CreatedAt: time.Unix(row.CreatedAt, 0), UpdatedAt: time.Unix(row.UpdatedAt, 0)})
	}
	return chats, nil
}

func (r *Repository) reserveChat(ctx context.Context, ownerID int64, title, key string) (Chat, error) {
	row, err := r.q.ReserveOwnerPikoChat(ctx, dbgen.ReserveOwnerPikoChatParams{OwnerID: ownerID, Title: title, CreatedAt: time.Now().Unix(), StartKey: sql.NullString{String: key, Valid: true}})
	return chatFromRow(row), storageError(err)
}

func messageFromRow(row dbgen.BuilderMessage) Message {
	return Message{Sequence: row.Sequence, Role: Role(row.Role), Content: row.Content, CreatedAt: time.Unix(row.CreatedAt, 0)}
}

func loadHistory(ctx context.Context, q *dbgen.Queries, ownerID, botID, chatID int64) (Conversation, error) {
	row, err := q.GetOwnerBuilderChat(ctx, dbgen.GetOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, ChatID: chatID})
	if err != nil {
		return Conversation{}, storageError(err)
	}
	rows, err := q.ListOwnerBuilderMessages(ctx, dbgen.ListOwnerBuilderMessagesParams{OwnerID: ownerID, BotID: botID, ChatID: chatID})
	if err != nil {
		return Conversation{}, err
	}
	history := Conversation{Chat: chatFromRow(row), Messages: make([]Message, 0, len(rows))}
	for _, row := range rows {
		history.Messages = append(history.Messages, messageFromRow(row))
	}
	return history, nil
}

func appendMessage(ctx context.Context, q *dbgen.Queries, ownerID, botID, chatID int64, role Role, content string) (Message, error) {
	now := time.Now().Unix()
	row, err := q.AppendOwnerBuilderMessage(ctx, dbgen.AppendOwnerBuilderMessageParams{OwnerID: ownerID, BotID: botID, ChatID: chatID, Role: string(role), Content: content, CreatedAt: now})
	if err != nil {
		return Message{}, storageError(err)
	}
	if err := q.TouchOwnerBuilderChat(ctx, dbgen.TouchOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, ChatID: chatID, UpdatedAt: now}); err != nil {
		return Message{}, err
	}
	return messageFromRow(row), nil
}
