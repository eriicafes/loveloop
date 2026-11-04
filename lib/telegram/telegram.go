// Package telegram is a small client for the parts of the Telegram Bot API
// LoveLoop uses.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
	"unicode/utf8"

	"github.com/eriicafes/got"
)

// MaxMessageLength is the longest text one message can carry.
const MaxMessageLength = 4096

// Update is an incoming update.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text,omitempty"`
	Caption   string `json:"caption,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// InlineKeyboardMarkup is a keyboard attached to a message.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// InlineKeyboardButton opens URL or sends CallbackData back to the bot.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	URL          string `json:"url,omitempty"`
	CallbackData string `json:"callback_data,omitempty"`
}

// BotCommand is a command shown in the bot's menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// Client calls the Bot API with a bot token.
type Client struct {
	Token      string
	BaseURL    string
	HTTPClient *http.Client
}

// New returns a Client for the bot with token.
func New(token string) *Client {
	return &Client{Token: token, BaseURL: "https://api.telegram.org", HTTPClient: &http.Client{Timeout: 70 * time.Second}}
}

// GetClient returns the Client for TELEGRAM_BOT_TOKEN.
var GetClient = got.Using(func(c *got.Container) *Client {
	return New(os.Getenv("TELEGRAM_BOT_TOKEN"))
})

// Error is an error the Bot API returned.
type Error struct {
	Method      string
	Code        int    `json:"error_code"`
	Description string `json:"description"`
	RetryAfter  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Call calls method with params and decodes its result into result, which
// may be nil.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/bot%s/%s", c.BaseURL, c.Token, method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer res.Body.Close()
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("telegram %s: decode response (%s): %w", method, res.Status, err)
	}
	if !envelope.OK {
		return &Error{Method: method, Code: envelope.ErrorCode, Description: envelope.Description, RetryAfter: envelope.Parameters.RetryAfter}
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, result)
}

// SendMessageParams are the parameters of SendMessage.
type SendMessageParams struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
	// ReplyParameters makes the message a reply quoting another one.
	ReplyParameters *ReplyParameters `json:"reply_parameters,omitempty"`
	// LinkPreviewOptions turns link previews off by default.
	LinkPreviewOptions map[string]any `json:"link_preview_options,omitempty"`
}

// ReplyParameters names the message a reply quotes.
type ReplyParameters struct {
	MessageID int64 `json:"message_id"`
}

// SendMessage sends a plain text message, split into several when it is
// longer than MaxMessageLength, and returns the first one. The keyboard goes
// on the last one, and the reply on the first.
func (c *Client) SendMessage(ctx context.Context, params SendMessageParams) (Message, error) {
	if params.LinkPreviewOptions == nil {
		params.LinkPreviewOptions = map[string]any{"is_disabled": true}
	}
	var first Message
	chunks := Split(params.Text, MaxMessageLength)
	for i, chunk := range chunks {
		part := params
		part.Text = chunk
		if i < len(chunks)-1 {
			part.ReplyMarkup = nil
		}
		if i > 0 {
			part.ReplyParameters = nil
		}
		var sent Message
		if err := c.Call(ctx, "sendMessage", part, &sent); err != nil {
			return first, err
		}
		if i == 0 {
			first = sent
		}
	}
	return first, nil
}

// SendChatAction shows an action such as "typing" in the chat.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	return c.Call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action}, nil)
}

// AnswerCallbackQuery acknowledges a button press, showing text briefly.
func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	return c.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// RemoveKeyboard removes the inline keyboard of a message.
func (c *Client) RemoveKeyboard(ctx context.Context, chatID, messageID int64) error {
	return c.Call(ctx, "editMessageReplyMarkup", map[string]any{"chat_id": chatID, "message_id": messageID, "reply_markup": InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{}}}, nil)
}

// SetWebhook points Telegram at url. Telegram sends secret in the
// X-Telegram-Bot-Api-Secret-Token header of every update.
func (c *Client) SetWebhook(ctx context.Context, url, secret string) error {
	return c.Call(ctx, "setWebhook", map[string]any{
		"url":             url,
		"secret_token":    secret,
		"allowed_updates": []string{"message", "callback_query"},
	}, nil)
}

// GetMe returns the bot's own user.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var user User
	return user, c.Call(ctx, "getMe", map[string]any{}, &user)
}

// WebhookInfo is the state of the bot's webhook.
type WebhookInfo struct {
	URL                  string `json:"url"`
	PendingUpdateCount   int    `json:"pending_update_count"`
	LastErrorDate        int64  `json:"last_error_date,omitempty"`
	LastErrorMessage     string `json:"last_error_message,omitempty"`
	MaxConnections       int    `json:"max_connections,omitempty"`
	HasCustomCertificate bool   `json:"has_custom_certificate"`
}

// GetWebhookInfo returns the state of the bot's webhook.
func (c *Client) GetWebhookInfo(ctx context.Context) (WebhookInfo, error) {
	var info WebhookInfo
	return info, c.Call(ctx, "getWebhookInfo", map[string]any{}, &info)
}

// DeleteWebhook stops webhook delivery, so GetUpdates can be used.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.Call(ctx, "deleteWebhook", map[string]any{}, nil)
}

// GetUpdates long-polls for updates after offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64) ([]Update, error) {
	var updates []Update
	err := c.Call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 50, "allowed_updates": []string{"message", "callback_query"}}, &updates)
	return updates, err
}

// SetMyCommands sets the bot's command menu.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	return c.Call(ctx, "setMyCommands", map[string]any{"commands": commands}, nil)
}

// Split splits text into chunks of at most limit bytes, preferring to break
// at newlines and spaces, and never inside a UTF-8 sequence.
func Split(text string, limit int) []string {
	var chunks []string
	for len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if i := bytes.LastIndexByte([]byte(text[:cut]), '\n'); i > limit/2 {
			cut = i + 1
		} else if i := bytes.LastIndexByte([]byte(text[:cut]), ' '); i > limit/2 {
			cut = i + 1
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return append(chunks, text)
}
