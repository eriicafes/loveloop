package web

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eriicafes/go-workflow"
	"github.com/eriicafes/go-workflow/worlds"
	"github.com/eriicafes/httpx/session"
	"github.com/eriicafes/loveloop/web/templates/pages"
	"github.com/eriicafes/loveloop/workflows"
	"github.com/eriicafes/tmpl"
)

// Helpers shared by the route handlers.

// render renders a page as HTML.
func (a *App) render(w http.ResponseWriter, status int, page tmpl.Template) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	return a.Templates.Render(w, page)
}

// redirectWithFlash sends the browser to path, showing flash once there.
func (a *App) redirectWithFlash(w http.ResponseWriter, r *http.Request, path string, flash pages.Flash) error {
	if err := a.Flash.Set(w, flash); err != nil {
		return err
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
	return nil
}

// cookies returns the session and flash cookies, signed with keys derived
// from password, so changing the password signs everyone out.
func cookies(password string, secure bool) (session.Cookie[Session], session.FlashCookie[pages.Flash], error) {
	sessionKey := sha256.Sum256([]byte("loveloop session " + password))
	flashKey := sha256.Sum256([]byte("loveloop flash " + password))
	sessionCookie, err := session.NewCookie[Session](session.DefaultCookieOptions("loveloop_session",
		session.WithCookieSecret(session.Secret(sessionKey[:])),
		session.WithCookieSecure(secure),
		session.WithCookieDuration(SessionDuration),
	))
	if err != nil {
		return nil, nil, err
	}
	flashCookie, err := session.NewFlashCookie[pages.Flash](session.DefaultCookieOptions("loveloop_flash",
		session.WithCookieSecret(session.Secret(flashKey[:])),
		session.WithCookieSecure(secure),
	))
	return sessionCookie, flashCookie, err
}

func parseUserIDs(list string) ([]int64, error) {
	var ids []int64
	for field := range strings.SplitSeq(list, ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		id, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			return nil, errors.New("TELEGRAM_ALLOWED_USERS must be comma separated Telegram user IDs")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// EnsureCheckIns starts a chain of CheckIns runs for the user, unless the
// one they have is still going. Each run starts the next day's run, so this
// only starts a chain for a new user or restarts one that failed.
func (a *App) EnsureCheckIns(ctx context.Context, chatID int64) error {
	if a.Store == nil {
		return errNoStore
	}
	ctx = workflow.NewContext(ctx, a.Client)
	id, err := a.Store.CheckInsRun(ctx, chatID)
	if err != nil {
		return err
	}
	if id != "" {
		run, err := workflow.Get[workflows.CheckInsResult](ctx, id)
		if err != nil {
			return err
		}
		status, err := run.Status(ctx)
		if err == nil && !status.Terminal() {
			return nil
		}
		if err != nil && !errors.Is(err, worlds.ErrRunNotFound) {
			return err
		}
	}
	// Two messages at once must not start two chains.
	key := fmt.Sprintf("checkins-start:%d", chatID)
	if first, err := a.Store.Once(ctx, key, time.Minute); err != nil || !first {
		return err
	}
	run, err := workflow.Start(ctx, workflows.CheckIns{ChatID: chatID, From: time.Now()})
	if err != nil {
		return errors.Join(err, a.Store.Release(ctx, key))
	}
	return a.Store.SetCheckInsRun(ctx, chatID, run.ID())
}
