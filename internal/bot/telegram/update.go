package telegram

import (
	"context"
	"encoding/json"
	"errors"
)

type User struct {
	ID    int64 `json:"id"`
	IsBot bool  `json:"is_bot"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	From User   `json:"from"`
	Chat Chat   `json:"chat"`
	Text string `json:"text"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       *int64    `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}

func DecodeUpdate(data []byte) (Update, error) {
	var u Update
	if json.Unmarshal(data, &u) != nil || u.ID == nil || *u.ID < 0 {
		return u, errors.New("invalid update")
	}
	return u, nil
}

type Action struct {
	Message      *SendMessage `json:"message,omitempty"`
	CallbackID   string       `json:"callback_id,omitempty"`
	CallbackText string       `json:"callback_text,omitempty"`
}

func (c *Client) Deliver(ctx context.Context, token string, a Action) error {
	if a.CallbackID != "" {
		var ok bool
		err := c.request(ctx, token, "answerCallbackQuery", struct {
			ID   string `json:"callback_query_id"`
			Text string `json:"text"`
		}{a.CallbackID, a.CallbackText}, &ok)
		// Expired/already answered callbacks cannot hold later message delivery hostage.
		if errors.Is(err, ErrRejected) {
			return nil
		}
		if err != nil {
			return err
		}
		if !ok {
			return ErrUnavailable
		}
		return nil
	}
	var message struct {
		ID int64 `json:"message_id"`
	}
	if err := c.request(ctx, token, "sendMessage", a.Message, &message); err != nil {
		return err
	}
	if message.ID <= 0 {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) Poll(ctx context.Context, token string, offset int64) ([]Update, error) {
	var updates []Update
	params := struct {
		Offset  int64    `json:"offset"`
		Limit   int      `json:"limit"`
		Timeout int      `json:"timeout"`
		Updates []string `json:"allowed_updates"`
	}{offset, 50, 0, []string{"message", "callback_query"}}
	err := c.request(ctx, token, "getUpdates", params, &updates)
	return updates, err
}
