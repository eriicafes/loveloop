// Package workflows declares LoveLoop's workflow types. Handlers start runs
// of them without importing their runners in workflows/flows.
//
// Every workflow here embeds workflow.StartOnLatest: nothing waits on their
// output, and each either ends within minutes or starts its own next run
// within days, so new runs start on the latest deployment and no run outlives
// Vercel's deployment retention.
package workflows

import (
	"time"

	"github.com/eriicafes/go-workflow"
)

// Conversation answers a user's mailbox in order: their messages, their
// answers to approval questions, and LoveLoop's own check-ins and
// reminders. One runs per user at a time, and it ends once the mailbox stays
// empty for a while. Use Deliver rather than starting it directly.
type Conversation struct {
	workflow.Struct[ConversationResult]
	workflow.StartOnLatest
	ChatID int64 `json:"chatId"`
}

type ConversationResult struct {
	// Turns is how many times the agent answered.
	Turns int `json:"turns"`
	// Failed is how many of those turns failed after telling the user.
	Failed int `json:"failed,omitempty"`
}

// CheckIns texts the user first a few times at random over the next day,
// within their waking hours.
type CheckIns struct {
	workflow.Struct[CheckInsResult]
	workflow.StartOnLatest
	ChatID int64 `json:"chatId"`
	// From starts the day the check-ins are spread over.
	From time.Time `json:"from"`
}

type CheckInsResult struct {
	Sent int `json:"sent"`
}

// Reminder texts the user about something at a time they asked for, and
// again every EveryDays days when it is set. Each run waits at most a few
// days and then starts the next run of the same reminder.
type Reminder struct {
	workflow.Struct[ReminderResult]
	workflow.StartOnLatest
	// ID is the reminder's ID, the first run's ID; empty in the first run.
	ID        string    `json:"id,omitempty"`
	ChatID    int64     `json:"chatId"`
	At        time.Time `json:"at"`
	About     string    `json:"about"`
	Person    string    `json:"person,omitempty"`
	EveryDays int       `json:"everyDays,omitempty"`
}

type ReminderResult struct {
	Fired int `json:"fired"`
}
