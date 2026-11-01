// Package workflows declares LoveLoop's workflow types. Handlers start runs
// of them without importing their runners in workflows/flows.
package workflows

import (
	"time"

	"github.com/eriicafes/go-workflow"
)

// Chat answers one message from the user, or their answer to a question the
// agent asked with buttons.
type Chat struct {
	workflow.Struct[ChatResult]
	ChatID int64  `json:"chatId"`
	Text   string `json:"text,omitempty"`
	// Approval answers a pending tool approval instead of Text.
	Approval *ApprovalAnswer `json:"approval,omitempty"`
}

// ApprovalAnswer is the user's tap on an approval button. Key is the short
// key of the approval ID that fits in Telegram callback data.
type ApprovalAnswer struct {
	Key      string `json:"key"`
	Approved bool   `json:"approved"`
}

type ChatResult struct {
	Reply string `json:"reply"`
}

// CheckIns texts the user first a few times at random over the next day,
// within their waking hours.
type CheckIns struct {
	workflow.Struct[CheckInsResult]
	ChatID int64 `json:"chatId"`
	// From starts the day the check-ins are spread over.
	From time.Time `json:"from"`
}

type CheckInsResult struct {
	Sent int `json:"sent"`
}

// Reminder texts the user about something at a time they asked for, and
// again every EveryDays days when it is set.
type Reminder struct {
	workflow.Struct[ReminderResult]
	ChatID    int64     `json:"chatId"`
	At        time.Time `json:"at"`
	About     string    `json:"about"`
	Person    string    `json:"person,omitempty"`
	EveryDays int       `json:"everyDays,omitempty"`
}

type ReminderResult struct {
	Fired int `json:"fired"`
}
