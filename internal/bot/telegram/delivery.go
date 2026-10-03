package telegram

import "context"

type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type Markup struct {
	Buttons [][]Button `json:"inline_keyboard"`
}
type SendMessage struct {
	ChatID int64   `json:"chat_id"`
	Text   string  `json:"text"`
	Markup *Markup `json:"reply_markup,omitempty"`
}

func (c *Client) SetWebhook(ctx context.Context, token, endpoint, secret string) error {
	params := struct {
		URL         string   `json:"url"`
		Secret      string   `json:"secret_token"`
		Drop        bool     `json:"drop_pending_updates"`
		Connections int      `json:"max_connections"`
		Updates     []string `json:"allowed_updates"`
	}{endpoint, secret, false, 1, []string{"message", "callback_query"}}
	var ok bool
	if err := c.request(ctx, token, "setWebhook", params, &ok); err != nil {
		return err
	}
	if !ok {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) DeleteWebhook(ctx context.Context, token string) error {
	var ok bool
	if err := c.request(ctx, token, "deleteWebhook", struct {
		Drop bool `json:"drop_pending_updates"`
	}{false}, &ok); err != nil {
		return err
	}
	if !ok {
		return ErrUnavailable
	}
	return nil
}
