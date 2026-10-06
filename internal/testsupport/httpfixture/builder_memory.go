package httpfixture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/tiktoken-go/tokenizer"
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
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	reply, err := codec.Count("پاسخ محفوظ")
	if err != nil {
		t.Fatal(err)
	}
	for turn := range 6 {
		prefix := fmt.Sprintf("تاریخچه اصلی %d", turn)
		padding := 10600
		if turn == 5 {
			base, err := codec.Count(prefix)
			if err != nil {
				t.Fatal(err)
			}
			// Cross 64k only once the sixth saved reply is included, preserving the
			// six ordinary paid runs before the tests exercise summary failures.
			padding = 64001 - total - base - reply - 8
		}
		message := MemoryText(prefix, padding)
		n, err := codec.Count(message)
		if err != nil {
			t.Fatal(err)
		}
		total += n + reply + 8
		if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {message}}); got.Code != 303 {
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
SELECT 1,n,CASE WHEN n%2=1 THEN 'owner' ELSE 'model' END,printf('legacy-history-%02d',n) || ?,1 FROM old`, strings.Repeat(" x", 6000)); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context(), a.DB, false); err != nil {
		t.Fatal(err)
	}
}

// Each space-prefixed x is one o200k token; this keeps threshold fixtures
// reproducible without relying on character-to-token ratios.
func MemoryText(prefix string, tokens int) string { return prefix + strings.Repeat(" x", tokens) }
