package pages

import (
	"github.com/eriicafes/tmpl"

	"github.com/eriicafes/loveloop/lib/telegram"
)

// Index shows whether the bot is connected, with forms to connect or
// disconnect its webhook, and the people LoveLoop remembers for each user.
type Index struct {
	// Status streams in once Telegram answers.
	Status tmpl.Async[Status, error]
	// People streams in once Redis answers.
	People tmpl.Async[[]UserPeople, error]
	Flash  Flash
}

// Status is the bot, its webhook, and what the forms need to act on them.
type Status struct {
	Bot        telegram.User
	BotError   string
	Webhook    telegram.WebhookInfo
	WebhookURL string
	// Connected reports whether Telegram delivers to WebhookURL.
	Connected bool
	LastError string
	PublicURL string
	// Users is the number of users, or -1 when it is unknown.
	Users int
}

func (p Index) Tmpl() tmpl.Template {
	return tmpl.Wrap(Layout{Title: "LoveLoop setup"}, tmpl.Tmpl("pages/index", p))
}

// UserPeople is a user and the loved ones LoveLoop remembers for them.
type UserPeople struct {
	Name   string
	People []Person
}

// Person is one loved one, described for the page.
type Person struct {
	Name         string
	Relationship string
	// LastContact says when the user last got in touch, such as "3 days
	// ago", or is empty when they never told LoveLoop.
	LastContact string
}

// Flash is a message shown once after a form posts.
type Flash struct {
	Message string `json:"message"`
	Error   bool   `json:"error,omitempty"`
}
