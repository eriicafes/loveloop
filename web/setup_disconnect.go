package web

import (
	"net/http"

	"github.com/eriicafes/loveloop/web/templates/pages"
)

// SetupDisconnect serves POST /setup/disconnect. It removes the webhook, so
// Telegram stops delivering messages.
func (a *App) SetupDisconnect(w http.ResponseWriter, r *http.Request) error {
	if err := a.Bot.DeleteWebhook(r.Context()); err != nil {
		return a.redirectWithFlash(w, r, "/", pages.Flash{Message: err.Error(), Error: true})
	}
	return a.redirectWithFlash(w, r, "/", pages.Flash{Message: "Disconnected. The bot won't receive messages until you connect it again."})
}
