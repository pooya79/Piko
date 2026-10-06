package httpfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
)

func MemoryReply(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5, "cost": 0.001},
	})
}

func FillMemoryChat(t *testing.T, b *Browser) {
	t.Helper()
	for turn := range 6 {
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {fmt.Sprintf("تاریخچه اصلی %d", turn)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	}
}

func SeedLegacyMemoryHistory(t *testing.T, a *HTTP) {
	t.Helper()
	RollbackToMigration(t, a.DB, "000015_builder_draft_outcomes")
	if _, err := a.DB.Exec(`WITH RECURSIVE old(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM old WHERE n<30)
INSERT INTO builder_messages (chat_id,sequence,role,content,created_at)
SELECT 1,n,CASE WHEN n%2=1 THEN 'owner' ELSE 'model' END,printf('legacy-history-%02d',n),1 FROM old`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
}
