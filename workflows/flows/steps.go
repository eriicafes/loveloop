// Package flows holds LoveLoop's workflow runners and the steps they call.
package flows

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"strings"

	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/go-workflow/workflowai"

	"github.com/eriicafes/loveloop/store"
	"github.com/eriicafes/loveloop/telegram"
)

// state is everything the agent knows about a user at the start of a turn.
type state struct {
	User      store.User                `json:"user"`
	People    []store.Person            `json:"people"`
	Reminders []store.Reminder          `json:"reminders"`
	History   []workflowai.ModelMessage `json:"history"`
	// Summary covers the messages folded out of History.
	Summary string `json:"summary"`
}

func (f *Flows) loadState(ctx context.Context, chatID int64) (state, error) {
	user, err := f.Store.User(ctx, chatID)
	if errors.Is(err, store.ErrNotFound) {
		return state{}, workflow.FatalError(err)
	} else if err != nil {
		return state{}, err
	}
	people, err := f.Store.People(ctx, chatID)
	if err != nil {
		return state{}, err
	}
	reminders, err := f.Store.Reminders(ctx, chatID)
	if err != nil {
		return state{}, err
	}
	history, err := f.Store.History(ctx, chatID)
	if err != nil {
		return state{}, err
	}
	summary, err := f.Store.Summary(ctx, chatID)
	if err != nil {
		return state{}, err
	}
	return state{User: user, People: people, Reminders: reminders, History: resolveApprovals(history), Summary: summary.Text}, nil
}

type historyAppend struct {
	ChatID   int64                     `json:"chatId"`
	Messages []workflowai.ModelMessage `json:"messages"`
}

func (f *Flows) appendHistory(ctx context.Context, input historyAppend) (struct{}, error) {
	return struct{}{}, f.Store.AppendHistory(ctx, input.ChatID, input.Messages)
}

// reply is a message to the user, with buttons for the agent's pending
// approvals.
type reply struct {
	ChatID    int64             `json:"chatId"`
	Text      string            `json:"text"`
	Approvals []pendingApproval `json:"approvals,omitempty"`
}

type pendingApproval struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

func (f *Flows) sendReply(ctx context.Context, r reply) (struct{}, error) {
	bot := f.Bot
	if text := strings.TrimSpace(r.Text); text != "" {
		if err := bot.SendMessage(ctx, telegram.SendMessageParams{ChatID: r.ChatID, Text: text}); err != nil {
			return struct{}{}, err
		}
	}
	for _, approval := range r.Approvals {
		key := approvalKey(approval.ID)
		err := bot.SendMessage(ctx, telegram.SendMessageParams{
			ChatID: r.ChatID,
			Text:   approval.Question,
			ReplyMarkup: &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
				{Text: "Yes 💔", CallbackData: "ap:y:" + key},
				{Text: "No, keep 💞", CallbackData: "ap:n:" + key},
			}}},
		})
		if err != nil {
			return struct{}{}, err
		}
	}
	return struct{}{}, nil
}

// approvalKey shortens an approval ID to fit Telegram's 64 byte callback
// data. Chat looks the ID up again in the conversation.
func approvalKey(id string) string {
	h := fnv.New64a()
	h.Write([]byte(id))
	return fmt.Sprintf("%016x", h.Sum64())
}

// draft is a text the agent suggests the user sends to a loved one.
type draft struct {
	Person string `json:"person" desc:"Name of the loved one the text is for, as saved."`
	Text   string `json:"text" desc:"The text itself, written in the user's voice, ready to send as is."`
}

// sendDraft shows a draft to the user with buttons that open it in WhatsApp
// or Telegram, ready to send to their loved one.
func (u userTools) sendDraft(ctx context.Context, in draft) (string, error) {
	person, err := u.Store.Person(ctx, u.ChatID, in.Person)
	if errors.Is(err, store.ErrNotFound) {
		person = store.Person{Name: in.Person}
	} else if err != nil {
		return "", err
	}
	text := in.Text
	var buttons []telegram.InlineKeyboardButton
	if phone := digits(person.Phone); phone != "" {
		buttons = append(buttons, telegram.InlineKeyboardButton{Text: "💬 WhatsApp", URL: "https://wa.me/" + phone + "?text=" + url.QueryEscape(text)})
	}
	if handle := strings.TrimPrefix(person.TelegramHandle, "@"); handle != "" {
		buttons = append(buttons, telegram.InlineKeyboardButton{Text: "✈️ Telegram", URL: "https://t.me/" + handle + "?text=" + url.QueryEscape(text)})
	} else {
		buttons = append(buttons, telegram.InlineKeyboardButton{Text: "✈️ Share", URL: "https://t.me/share/url?url=" + url.QueryEscape(text)})
	}
	err = u.Bot.SendMessage(ctx, telegram.SendMessageParams{
		ChatID:      u.ChatID,
		Text:        fmt.Sprintf("💌 For %s:\n\n%s", person.Name, text),
		ReplyMarkup: &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{buttons}},
	})
	if err != nil {
		return "", err
	}
	return "The draft is shown to the user with buttons to send it. Don't repeat it in your reply.", nil
}

func digits(phone string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
}
