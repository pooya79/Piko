package bot

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"modernc.org/sqlite"
)

type Repository struct {
	q  *dbgen.Queries
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{q: dbgen.New(db), db: db} }
func (r *Repository) create(ctx context.Context, ownerID int64, identity telegram.Identity, delivery telegram.Delivery, encrypted []byte) (Bot, error) {
	var webhook int64
	if delivery.HasWebhook {
		webhook = 1
	}
	row, err := r.q.CreateBot(ctx, dbgen.CreateBotParams{OwnerID: ownerID, TelegramID: sql.NullInt64{Int64: identity.ID, Valid: true}, Name: identity.Name, Username: identity.Username, EncryptedToken: encrypted, HasWebhook: webhook, PendingUpdates: delivery.PendingUpdates, VerifiedAt: time.Now().Unix()})
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == 2067 {
			return Bot{}, ErrExists
		}
		return Bot{}, err
	}
	return r.get(ctx, ownerID, row.ID)
}
func (r *Repository) list(ctx context.Context, ownerID int64) ([]Bot, error) {
	rows, err := r.q.ListOwnerBots(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	bots := make([]Bot, 0, len(rows))
	for _, row := range rows {
		bots = append(bots, botFromRow(dbgen.GetOwnerBotRow(row)))
	}
	return bots, nil
}
func (r *Repository) get(ctx context.Context, ownerID, id int64) (Bot, error) {
	row, err := r.q.GetOwnerBot(ctx, dbgen.GetOwnerBotParams{OwnerID: ownerID, ID: id})
	if err != nil {
		return Bot{}, err
	}
	return botFromRow(dbgen.GetOwnerBotRow(row)), nil
}

// sqlc gives the identical public projection a distinct type for each query.
// Normalize it here so credential-free Bot mapping has one definition.
func botFromRow(row dbgen.GetOwnerBotRow) Bot {
	return Bot{ID: row.ID, TelegramID: row.TelegramID.Int64, Name: row.Name, Username: row.Username,
		HasWebhook: row.HasWebhook != 0, PendingUpdates: row.PendingUpdates, VerifiedAt: time.Unix(row.VerifiedAt, 0), PublishedVersion: row.PublishedVersion, DeliveryState: row.DeliveryState, DeliveryMode: DeliveryMode(row.DeliveryMode), DeliveryError: row.DeliveryError != 0, WebhookIsPiko: row.WebhookIsPiko != 0, Paused: row.Paused != 0, Disconnected: row.Disconnected != 0, Unconnected: row.Unconnected != 0, HasImplementation: row.HasImplementation != 0}
}
