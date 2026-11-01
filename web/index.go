package web

import (
	"cmp"
	"net/http"
	"os"
	"time"

	"github.com/eriicafes/loveloop/web/templates/pages"
)

// index serves GET /, the setup page: whether the bot is configured and
// connected, with forms to connect or disconnect its webhook.
func (a *App) index(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	page := pages.Index{PublicURL: publicURL(r), Checks: checks(), Users: -1}
	page.WebhookURL = page.PublicURL + WebhookPath
	page.Flash, _ = a.Flash.Get(w, r)

	if os.Getenv("TELEGRAM_BOT_TOKEN") == "" {
		page.BotError = "TELEGRAM_BOT_TOKEN is not set."
	} else if me, err := a.Bot.GetMe(ctx); err != nil {
		page.BotError = err.Error()
	} else {
		page.Bot = me
		if info, err := a.Bot.GetWebhookInfo(ctx); err != nil {
			page.BotError = err.Error()
		} else {
			page.Webhook = info
			page.Connected = info.URL != "" && info.URL == page.WebhookURL
			if info.LastErrorMessage != "" {
				page.LastError = info.LastErrorMessage + " (" + time.Unix(info.LastErrorDate, 0).UTC().Format("2 Jan 15:04 UTC") + ")"
			}
		}
	}
	if a.Store != nil {
		if users, err := a.Store.Users(ctx); err == nil {
			page.Users = len(users)
		}
	}
	return a.render(w, http.StatusOK, page)
}

// publicURL guesses the URL the bot is deployed at: the production domain
// on Vercel, and otherwise the URL of this request.
func publicURL(r *http.Request) string {
	if domain := os.Getenv("VERCEL_PROJECT_PRODUCTION_URL"); domain != "" {
		return "https://" + domain
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return cmp.Or(r.Header.Get("X-Forwarded-Proto"), scheme) + "://" + r.Host
}

func checks() []pages.Check {
	set := func(name string) bool { return os.Getenv(name) != "" }
	model := "AI Gateway API key"
	switch {
	case set("AI_GATEWAY_API_KEY"):
	case set("ANTHROPIC_API_KEY"):
		model = "Anthropic API key"
	case set("VERCEL_OIDC_TOKEN"):
		model = "AI Gateway via Vercel OIDC"
	default:
		model = "Set AI_GATEWAY_API_KEY or ANTHROPIC_API_KEY, or deploy on Vercel"
	}
	return []pages.Check{
		{Name: "Bot token", Detail: "TELEGRAM_BOT_TOKEN", OK: set("TELEGRAM_BOT_TOKEN")},
		{Name: "Webhook secret", Detail: "TELEGRAM_WEBHOOK_SECRET", OK: set("TELEGRAM_WEBHOOK_SECRET")},
		{Name: "Redis", Detail: "REDIS_URL", OK: set("REDIS_URL")},
		{Name: "Workflow Redis", Detail: "UPSTASH_REDIS_REST_URL and UPSTASH_REDIS_REST_TOKEN", OK: set("UPSTASH_REDIS_REST_URL") && set("UPSTASH_REDIS_REST_TOKEN")},
		{Name: "Model", Detail: model, OK: set("AI_GATEWAY_API_KEY") || set("ANTHROPIC_API_KEY") || set("VERCEL_OIDC_TOKEN")},
		{Name: "Approval signing", Detail: "TOOL_APPROVAL_SECRET", OK: set("TOOL_APPROVAL_SECRET"), Optional: true},
		{Name: "Allowed users", Detail: cmp.Or(os.Getenv("TELEGRAM_ALLOWED_USERS"), "Anyone can chat"), OK: set("TELEGRAM_ALLOWED_USERS"), Optional: true},
		{Name: "Long timers", Detail: "QSTASH_TOKEN", OK: set("QSTASH_TOKEN"), Optional: true},
	}
}
