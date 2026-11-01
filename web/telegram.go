package web

import (
	"cmp"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/httpx"
	"github.com/eriicafes/httpx/httperrors"

	"github.com/eriicafes/loveloop/telegram"
	"github.com/eriicafes/loveloop/workflows"
)

// WebhookPath is the path Telegram delivers updates to.
const WebhookPath = "/telegram"

var errNoStore = errors.New("REDIS_URL is not set")

// telegram serves POST /telegram, Telegram's webhook. It answers as soon as
// it has started the run that handles the update, since Telegram retries
// slow webhooks. A failed update answers 500, so Telegram redelivers it, and
// redeliveries of handled updates are deduplicated.
func (a *App) telegram(w http.ResponseWriter, r *http.Request) error {
	if a.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(a.WebhookSecret)) != 1 {
		return httperrors.New("unauthorized", http.StatusUnauthorized)
	}
	update, err := httpx.Read[telegram.Update](r)
	if err != nil {
		return httperrors.Wrap(err, "invalid update", http.StatusBadRequest)
	}
	if err := a.HandleUpdate(r.Context(), update); err != nil {
		return httpx.InternalError(fmt.Errorf("telegram update %d: %w", update.UpdateID, err), "failed")
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

// HandleUpdate starts the run that answers a Telegram update.
func (a *App) HandleUpdate(ctx context.Context, update telegram.Update) error {
	if a.Store == nil {
		return errNoStore
	}
	ctx = workflow.NewContext(ctx, a.Client)
	key := "update:" + strconv.FormatInt(update.UpdateID, 10)
	if first, err := a.Store.Once(ctx, key, 24*time.Hour); err != nil || !first {
		return err
	}
	var err error
	switch {
	case update.CallbackQuery != nil:
		err = a.handleCallback(ctx, *update.CallbackQuery)
	case update.Message != nil:
		err = a.handleMessage(ctx, *update.Message)
	}
	if err != nil {
		return errors.Join(err, a.Store.Release(ctx, key))
	}
	return nil
}

func (a *App) handleMessage(ctx context.Context, message telegram.Message) error {
	chatID := message.Chat.ID
	if message.Chat.Type != "private" || message.From == nil {
		return nil
	}
	if len(a.AllowedUsers) > 0 && !slices.Contains(a.AllowedUsers, message.From.ID) {
		return a.Bot.SendMessage(ctx, telegram.SendMessageParams{ChatID: chatID, Text: "Sorry, LoveLoop is invite only for now 💌"})
	}
	if _, _, err := a.Store.EnsureUser(ctx, chatID, message.From.FirstName, time.Now()); err != nil {
		return err
	}
	if err := a.EnsureCheckIns(ctx, chatID); err != nil {
		log.Printf("check-ins for %d: %v", chatID, err)
	}

	text := strings.TrimSpace(cmp.Or(message.Text, message.Caption))
	switch command, _, _ := strings.Cut(text, " "); command {
	case "":
		return a.Bot.SendMessage(ctx, telegram.SendMessageParams{ChatID: chatID, Text: "I can only read text for now, but I'm sure it was adorable 🥰"})
	case "/reset":
		if err := a.Store.ClearHistory(ctx, chatID); err != nil {
			return err
		}
		return a.Bot.SendMessage(ctx, telegram.SendMessageParams{ChatID: chatID, Text: "Fresh start! I cleared our chat, but I still remember all your people 💞"})
	case "/start":
		text = "Hi! I just started chatting with you. Introduce yourself briefly and ask what I'd like you to call me."
	case "/people":
		text = "Give me a fun rundown of everyone you remember and how I'm doing with each of them."
	case "/reminders":
		text = "What reminders do I have coming up?"
	case "/pause":
		text = "Please pause your daily check-ins for now."
	case "/resume":
		text = "Please resume your daily check-ins."
	}

	_ = a.Bot.SendChatAction(ctx, chatID, "typing")
	_, err := workflow.Start(ctx, workflows.Chat{ChatID: chatID, Text: text})
	return err
}

// handleCallback handles a tap on an approval button, whose data is
// "ap:<y|n>:<key>".
func (a *App) handleCallback(ctx context.Context, query telegram.CallbackQuery) error {
	parts := strings.SplitN(query.Data, ":", 3)
	if query.Message == nil || len(parts) != 3 || parts[0] != "ap" {
		return a.Bot.AnswerCallbackQuery(ctx, query.ID, "")
	}
	approved := parts[1] == "y"
	_ = a.Bot.AnswerCallbackQuery(ctx, query.ID, map[bool]string{true: "Okay 💔", false: "Kept safe 💞"}[approved])
	_ = a.Bot.RemoveKeyboard(ctx, query.Message.Chat.ID, query.Message.MessageID)
	_ = a.Bot.SendChatAction(ctx, query.Message.Chat.ID, "typing")
	_, err := workflow.Start(ctx, workflows.Chat{
		ChatID:   query.Message.Chat.ID,
		Approval: &workflows.ApprovalAnswer{Key: parts[2], Approved: approved},
	})
	return err
}
