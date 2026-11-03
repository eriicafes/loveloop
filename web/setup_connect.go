package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/eriicafes/loveloop/lib/telegram"
	"github.com/eriicafes/loveloop/web/templates/pages"
)

// commands is the bot's command menu.
var commands = []telegram.BotCommand{
	{Command: "people", Description: "Everyone I remember"},
	{Command: "reminders", Description: "Upcoming reminders"},
	{Command: "pause", Description: "Pause daily check-ins"},
	{Command: "resume", Description: "Resume daily check-ins"},
	{Command: "reset", Description: "Clear our chat (I keep your people)"},
}

// SetupConnect serves POST /setup/connect. It points Telegram's webhook at
// the public URL in the form and sets the bot's command menu.
func (a *App) SetupConnect(w http.ResponseWriter, r *http.Request) error {
	fail := func(message string) error {
		return a.redirectWithFlash(w, r, "/", pages.Flash{Message: message, Error: true})
	}
	base, err := parsePublicURL(r.FormValue("public_url"))
	if err != nil {
		return fail(err.Error())
	}
	if a.WebhookSecret == "" {
		return fail("Set TELEGRAM_WEBHOOK_SECRET first.")
	}
	if err := a.Bot.SetWebhook(r.Context(), base+TelegramWebhookPath, a.WebhookSecret); err != nil {
		return fail(err.Error())
	}
	if err := a.Bot.SetMyCommands(r.Context(), commands); err != nil {
		return fail("The webhook is connected, but setting commands failed: " + err.Error())
	}
	return a.redirectWithFlash(w, r, "/", pages.Flash{Message: "Connected! Telegram now delivers messages to " + base + TelegramWebhookPath + " 💞"})
}

func parsePublicURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", errors.New("enter the full URL the bot is deployed at, like https://loveloop.vercel.app")
	}
	if u.Scheme != "https" {
		return "", errors.New("Telegram only delivers to https URLs")
	}
	return "https://" + u.Host + strings.TrimSuffix(u.Path, "/"), nil
}
