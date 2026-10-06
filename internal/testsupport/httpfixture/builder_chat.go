package httpfixture

import (
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

// SeedBotChat arranges existing saved work without admitting a paid Builder run.
// Empty historical chats remain valid even though the studio no longer creates them.
func SeedBotChat(t *testing.T, db *sql.DB, botID int64, title string) string {
	t.Helper()
	var ownerID int64
	if err := db.QueryRow("SELECT owner_id FROM bots WHERE id = ?", botID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	chat, err := dbgen.New(db).CreateOwnerBuilderChat(t.Context(), dbgen.CreateOwnerBuilderChatParams{OwnerID: ownerID, BotID: botID, Title: title, CreatedAt: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return "/bots/" + strconv.FormatInt(botID, 10) + "/chats/" + strconv.FormatInt(chat.ID, 10)
}
