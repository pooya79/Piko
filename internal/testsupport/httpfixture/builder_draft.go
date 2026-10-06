package httpfixture

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Script the external OpenRouter wire protocol; generation still uses real Genkit.
func BuilderToolReply(w http.ResponseWriter, name string, input any) {
	args, _ := json.Marshal(input)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
		"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
			"id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": string(args)},
		}}}, "finish_reason": "tool_calls",
	}}, "usage": map[string]any{"total_tokens": 5}})
}

const EditedMenuDraft = `{"version":1,"welcome":{"id":"welcome","type":"message","text":"سلام از گفتگو"},"menu":{"id":"menu","type":"menu","text":"انتخاب کنید","choices":[{"id":"hours","label":"ساعت تازه","target":"hours-reply"},{"id":"contact","label":"تماس","target":"contact-reply"}]},"messages":[{"id":"hours-reply","type":"message","text":"۱۰ تا ۱۸"},{"id":"contact-reply","type":"message","text":"02112345678"}]}`

// Read tool responses as an external provider would; never reach into Builder state.
func ProviderDraft(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
		return nil
	}
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		var out struct {
			Definition map[string]any `json:"definition"`
		}
		var content string
		if json.Unmarshal(m.Content, &content) == nil && json.Unmarshal([]byte(content), &out) == nil && out.Definition != nil {
			return out.Definition
		}
	}
	t.Error("authorized Draft tool response missing")
	return nil
}

func BuilderTextReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"منو را آماده کردم؛ آن را در پیش\u200cنمایش امتحان کنید."},"finish_reason":"stop"}],"usage":{"total_tokens":7}}`))
}
