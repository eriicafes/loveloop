// Package web serves LoveLoop's HTTP routes through App: Telegram's webhook,
// and the setup page that connects the bot to it, rendered with tmpl and
// styled with Tailwind built by Vite. Each route lives in the file named
// after its path, and Handler collects them.
package web

import (
	"cmp"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/got"
	"github.com/eriicafes/httpx"
	"github.com/eriicafes/httpx/httperrors"
	"github.com/eriicafes/httpx/session"
	"github.com/eriicafes/tmpl"
	"github.com/eriicafes/tmpl/vite"

	"github.com/eriicafes/loveloop/lib/store"
	"github.com/eriicafes/loveloop/lib/telegram"
	"github.com/eriicafes/loveloop/web/templates"
	"github.com/eriicafes/loveloop/web/templates/pages"
)

// dist is the Vite build output, built with `pnpm build` in this directory.
//
//go:embed all:dist
var dist embed.FS

// App serves LoveLoop's routes with its dependencies.
type App struct {
	// Store is nil when REDIS_URL is not set, so the setup page still
	// shows what is missing.
	Store *store.Store
	Bot   *telegram.Client
	// Client starts and reads runs. GetApp leaves it unset: the entry point
	// sets it on the World it runs with.
	Client    workflow.Client
	Vite      *vite.Vite
	Templates *tmpl.Templates
	// Session marks a browser signed in to the setup page, and Flash
	// carries a message across the redirect after a form posts. Both are
	// signed with keys derived from Password.
	Session session.Cookie[Session]
	Flash   session.FlashCookie[pages.Flash]
	// Password is SETUP_PASSWORD; an empty password disables sign in.
	Password      string
	WebhookSecret string
	// AllowedUsers lists the Telegram users who may chat; empty allows all.
	AllowedUsers []int64
	// QStash serves the Vercel World's signed long timer callback, and
	// Workflow serves the local World's RPC endpoint, which has no
	// authentication. Entry points set them; Handler mounts those set.
	QStash   http.Handler
	Workflow http.Handler
}

// Session is the signed-in state of a browser.
type Session struct {
	ExpiresAt time.Time `json:"expiresAt"`
}

// SessionDuration is how long a sign in lasts.
const SessionDuration = 7 * 24 * time.Hour

// GetVite returns Vite serving the embedded build. Local development mocks
// it with one that loads assets from the Vite dev server.
var GetVite = got.TryUsing(func(c *got.Container) (*vite.Vite, error) {
	output, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return vite.New(vite.Config{Output: output})
})

// GetApp returns the App configured from the environment.
var GetApp = got.TryUsing(func(c *got.Container) (*App, error) {
	app := &App{
		Bot:           telegram.GetClient.From(c),
		Password:      os.Getenv("SETUP_PASSWORD"),
		WebhookSecret: os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
	}
	var err error
	if app.Store, err = store.GetStore.From(c); err != nil && os.Getenv("REDIS_URL") != "" {
		return nil, err
	}
	if app.Vite, err = GetVite.From(c); err != nil {
		return nil, err
	}
	if app.Templates, err = templates.Parse(app.Vite); err != nil {
		return nil, err
	}
	if app.AllowedUsers, err = parseUserIDs(os.Getenv("TELEGRAM_ALLOWED_USERS")); err != nil {
		return nil, err
	}
	if app.Session, app.Flash, err = cookies(app.Password, os.Getenv("VERCEL") != ""); err != nil {
		return nil, err
	}
	return app, nil
})

// Handler returns every route. Form posts from other origins are rejected,
// and the setup routes redirect to /login until the browser signs in.
// Assets from the Vite build are served before the routes.
func (a *App) Handler() http.Handler {
	mux := httpx.Fallback(http.NewServeMux(), a.handleError)
	mux.Route("POST "+TelegramWebhookPath, a.telegram)

	site := httpx.Use(mux, http.NewCrossOriginProtection().Handler)
	site.Route("GET /login", a.loginPage)
	site.Route("POST /login", a.login)
	site.Route("POST /logout", a.logout)

	setup := httpx.Use(site, a.requireSession)
	setup.Route("GET /{$}", a.index)
	setup.Route("POST /setup/connect", a.setupConnect)
	setup.Route("POST /setup/disconnect", a.setupDisconnect)

	if a.QStash != nil {
		mux.Handle("POST /v1/workflow/callbacks/qstash", a.QStash)
	}
	if a.Workflow != nil {
		mux.Handle("/v1/workflow/", a.Workflow)
	}
	return a.Vite.ServePublic(workflow.Middleware(mux, a.Client))
}

// handleError answers with an HTTP error's own status and message, and
// logs any other error and answers 500, so Telegram redelivers a failed
// update.
func (a *App) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if httpErr, ok := httperrors.Unwrap(err); ok {
		message, status, _ := httperrors.Parse(httpErr)
		http.Error(w, message, status)
		return
	}
	log.Printf("%s %s: %v", r.Method, r.URL.Path, cmp.Or(errors.Unwrap(err), err))
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
