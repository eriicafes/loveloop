// Package flows holds LoveLoop's workflow runners and the steps they call.
package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode"

	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/go-workflow/workflowai"

	"github.com/eriicafes/loveloop/lib/store"
	"github.com/eriicafes/loveloop/lib/telegram"
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

// reply is a message to the user, then the drafts the agent wrote, then
// buttons for the agent's pending approvals.
type reply struct {
	ChatID    int64             `json:"chatId"`
	Text      string            `json:"text"`
	Drafts    []queuedDraft     `json:"drafts,omitempty"`
	Approvals []pendingApproval `json:"approvals,omitempty"`
}

type pendingApproval struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

func (f *Flows) sendReply(ctx context.Context, r reply) (struct{}, error) {
	bot := f.Bot
	if text := strings.TrimSpace(r.Text); text != "" {
		if _, err := bot.SendMessage(ctx, telegram.SendMessageParams{ChatID: r.ChatID, Text: text}); err != nil {
			return struct{}{}, err
		}
	}
	for _, d := range r.Drafts {
		// The draft is a message of its own so it copies cleanly, and the
		// reply under it says who it's for.
		sent, err := bot.SendMessage(ctx, telegram.SendMessageParams{ChatID: r.ChatID, Text: d.Text})
		if err != nil {
			return struct{}{}, err
		}
		_, err = bot.SendMessage(ctx, telegram.SendMessageParams{
			ChatID:          r.ChatID,
			Text:            "For " + d.Person,
			ReplyParameters: &telegram.ReplyParameters{MessageID: sent.MessageID},
		})
		if err != nil {
			return struct{}{}, err
		}
	}
	for _, approval := range r.Approvals {
		key := approvalKey(approval.ID)
		_, err := bot.SendMessage(ctx, telegram.SendMessageParams{
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

// draft is a message the agent writes for the user to send someone in their
// life.
type draft struct {
	Person string `json:"person" desc:"The person it's for, as saved."`
	Text   string `json:"text" desc:"The message, in the user's voice, ready to copy and send as is."`
}

// queuedDraft is a checked draft, sent after the agent's reply. Person is
// the name the user knows them by.
type queuedDraft struct {
	Person string `json:"person"`
	Text   string `json:"text"`
	Note   string `json:"note"`
}

// queueDraft checks a draft and sets the person's follow-up so the agent asks
// how it went. The draft is sent after the agent's reply, which ends with
// it: the agent writes nothing after a draft. LoveLoop never sends it to the
// person: the user does.
func (u userTools) queueDraft(ctx context.Context, in draft) (queuedDraft, error) {
	text := strings.TrimSpace(in.Text)
	if err := checkDraft(text); err != nil {
		return queuedDraft{}, workflow.FatalError(err)
	}
	name := strings.TrimSpace(in.Person)
	person, err := u.Store.Person(ctx, u.ChatID, name)
	if err == nil {
		name = person.Name
		person.FollowUp = fmt.Sprintf("you drafted %q for the user to send them", text)
		person.FollowUpAt = time.Now()
		err = u.Store.SavePerson(ctx, u.ChatID, person)
	} else if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return queuedDraft{}, err
	}
	note := fmt.Sprintf("The user gets it after your words as its own message, then a reply tagging it that says %q. Your turn ends here.", "For "+name)
	return queuedDraft{Person: name, Text: text, Note: note}, nil
}

// draftsIn returns the drafts the agent queued in result, in order.
func draftsIn(result workflowai.Result) []queuedDraft {
	var drafts []queuedDraft
	for _, step := range result.Steps {
		for _, tool := range step.ToolResults {
			var d queuedDraft
			if tool.ToolName == "draftText" && !tool.IsError && json.Unmarshal(tool.Output, &d) == nil && d.Text != "" {
				drafts = append(drafts, d)
			}
		}
	}
	return drafts
}

// checkDraft rejects a draft that doesn't read like a real text: one with em
// or en dashes, semicolons, or a colon before a space, as in a label. Colons
// in times such as 7:30 and in emoticons such as :) are fine.
func checkDraft(text string) error {
	if text == "" {
		return errors.New("the message is empty")
	}
	if strings.ContainsAny(text, "—–;") {
		return errors.New("rewrite the message without em dashes, en dashes or semicolons, the way people really text")
	}
	for i, r := range text {
		if r == ':' && (i+1 == len(text) || unicode.IsSpace(rune(text[i+1]))) {
			return errors.New("rewrite the message without colons, the way people really text")
		}
	}
	return nil
}
