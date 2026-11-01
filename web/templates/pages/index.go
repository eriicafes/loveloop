package pages

import (
	"github.com/eriicafes/tmpl"

	"github.com/eriicafes/loveloop/telegram"
)

// Index shows whether the bot is configured and connected, with forms to
// connect or disconnect its webhook.
type Index struct {
	Bot        telegram.User
	BotError   string
	Webhook    telegram.WebhookInfo
	WebhookURL string
	// Connected reports whether Telegram delivers to WebhookURL.
	Connected bool
	LastError string
	PublicURL string
	// Users is the number of users, or -1 when it is unknown.
	Users  int
	Checks []Check
	Flash  Flash
}

func (p Index) Tmpl() tmpl.Template {
	return tmpl.Wrap(Layout{Title: "LoveLoop setup"}, tmpl.Tmpl("pages/index", p))
}

// Check is one piece of configuration and whether it is set.
type Check struct {
	Name     string
	Detail   string
	OK       bool
	Optional bool
}

// Flash is a message shown once after a form posts.
type Flash struct {
	Message string `json:"message"`
	Error   bool   `json:"error,omitempty"`
}
