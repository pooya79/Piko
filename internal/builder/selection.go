package builder

import (
	"encoding/json"
	"errors"

	"github.com/pooya79/Piko/internal/bot/flow"
)

var ErrSelection = errors.New("selected Block is stale or removed")

// Selection is an inspection reference, validated against the admitted snapshot.
type Selection struct {
	Key      string
	Revision int64
}

func (s Selection) context(botID, revision int64, definition string) (string, error) {
	if s.Key == "" && s.Revision == 0 {
		return "", nil
	}
	if botID == 0 || revision != s.Revision {
		return "", ErrSelection
	}
	d, err := flow.Decode(definition)
	if err != nil {
		return "", err
	}
	n, ok := flow.ProjectCanvas(d).Block(s.Key)
	if !ok {
		return "", ErrSelection
	}
	data, err := json.Marshal(struct {
		BotID    int64           `json:"bot_id"`
		Revision int64           `json:"revision"`
		Key      string          `json:"key"`
		Kind     string          `json:"kind"`
		ID       string          `json:"id"`
		FormID   string          `json:"form_id,omitempty"`
		Value    json.RawMessage `json:"value"`
	}{botID, revision, n.Key, n.Kind, n.ID, n.FormID, json.RawMessage(n.Context)})
	return string(data), err
}
