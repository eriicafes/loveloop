package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/eriicafes/tmpl"

	"github.com/eriicafes/loveloop/web/templates/pages"
)

// Index serves GET /, the setup page: whether the bot is connected, with
// forms to connect or disconnect its webhook, and each user's people.
func (a *App) Index(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	page := pages.Index{
		Status: tmpl.Go(func(status tmpl.Async[pages.Status, error]) {
			status.Ok(a.status(ctx, publicURL(r)))
		}),
		People: tmpl.Go(func(people tmpl.Async[[]pages.UserPeople, error]) {
			if found, err := a.people(ctx, time.Now()); err != nil {
				people.Err(err)
			} else {
				people.Ok(found)
			}
		}),
	}
	page.Flash, _ = a.Flash.Get(w, r)
	return a.stream(w, r, http.StatusOK, page)
}

// status asks Telegram about the bot and its webhook, which can take a
// moment, so the page streams it in.
func (a *App) status(ctx context.Context, publicURL string) pages.Status {
	status := pages.Status{PublicURL: publicURL, WebhookURL: publicURL + TelegramWebhookPath, Users: -1}
	if os.Getenv("TELEGRAM_BOT_TOKEN") == "" {
		status.BotError = "TELEGRAM_BOT_TOKEN is not set."
	} else if me, err := a.Bot.GetMe(ctx); err != nil {
		status.BotError = err.Error()
	} else {
		status.Bot = me
		if info, err := a.Bot.GetWebhookInfo(ctx); err != nil {
			status.BotError = err.Error()
		} else {
			status.Webhook = info
			status.Connected = info.URL != "" && info.URL == status.WebhookURL
			if info.LastErrorMessage != "" {
				status.LastError = info.LastErrorMessage + " (" + time.Unix(info.LastErrorDate, 0).UTC().Format("2 Jan 15:04 UTC") + ")"
			}
		}
	}
	if a.Store != nil {
		if users, err := a.Store.Users(ctx); err == nil {
			status.Users = len(users)
		}
	}
	return status
}

// people returns every user, by name, with the loved ones LoveLoop
// remembers for them.
func (a *App) people(ctx context.Context, now time.Time) ([]pages.UserPeople, error) {
	if a.Store == nil {
		return nil, errNoStore
	}
	ids, err := a.Store.Users(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]pages.UserPeople, 0, len(ids))
	for _, id := range ids {
		user, err := a.Store.User(ctx, id)
		if err != nil {
			return nil, err
		}
		people, err := a.Store.People(ctx, id)
		if err != nil {
			return nil, err
		}
		entry := pages.UserPeople{Name: cmp.Or(user.Name, fmt.Sprintf("Chat %d", id))}
		for _, person := range people {
			entry.People = append(entry.People, pages.Person{
				Name:         person.Name,
				Relationship: person.Relationship,
				LastContact:  lastContact(person.LastContactAt, now),
			})
		}
		result = append(result, entry)
	}
	slices.SortFunc(result, func(a, b pages.UserPeople) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return result, nil
}

// lastContact describes when at was, relative to now, in days.
func lastContact(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	switch days := int(now.Sub(at).Hours() / 24); days {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
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
