package telebeans

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Telegram struct {
	Base string
	HTTP *http.Client
}
type User struct {
	ID int64 `json:"id"`
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
	Date int64  `json:"date"`
}
type Callback struct {
	ID      string  `json:"id"`
	From    User    `json:"from"`
	Message Message `json:"message"`
	Data    string  `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}
type Keyboard [][]Button

func NewTelegram(token string) *Telegram {
	return &Telegram{Base: "https://api.telegram.org/bot" + token, HTTP: &http.Client{Timeout: 35 * time.Second}}
}
func (t *Telegram) call(ctx context.Context, method string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.Base+"/"+method, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("Telegram: invalid API URL")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		// net/http URL errors include the bot token. Report only the underlying error.
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return fmt.Errorf("Telegram %s: %v", method, err)
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		return fmt.Errorf("Telegram %s: invalid response (HTTP %d)", method, resp.StatusCode)
	}
	if !result.OK {
		return fmt.Errorf("Telegram %s: %s", method, result.Description)
	}
	if out != nil {
		return json.Unmarshal(result.Result, out)
	}
	return nil
}
func (t *Telegram) Updates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var updates []Update
	err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeout, "allowed_updates": []string{"message", "callback_query"}}, &updates)
	return updates, err
}
func (t *Telegram) Identity(ctx context.Context) error {
	return t.call(ctx, "getMe", map[string]any{}, nil)
}
func (t *Telegram) Send(ctx context.Context, chat int64, text string, k Keyboard) (Message, error) {
	var m Message
	body := map[string]any{"chat_id": chat, "text": text}
	if k != nil {
		body["reply_markup"] = map[string]any{"inline_keyboard": k}
	}
	err := t.call(ctx, "sendMessage", body, &m)
	return m, err
}
func (t *Telegram) Edit(ctx context.Context, chat, message int64, text string, k Keyboard) error {
	if k == nil {
		k = Keyboard{}
	}
	err := t.call(ctx, "editMessageText", map[string]any{"chat_id": chat, "message_id": message, "text": text, "reply_markup": map[string]any{"inline_keyboard": k}}, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}
func (t *Telegram) Answer(ctx context.Context, id string) error {
	return t.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id}, nil)
}
