package web

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/eriicafes/loveloop/web/templates/pages"
)

// loginPage serves GET /login.
func (a *App) loginPage(w http.ResponseWriter, r *http.Request) error {
	if a.signedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return nil
	}
	return a.render(w, http.StatusOK, pages.Login{Configured: a.Password != ""})
}

// login serves POST /login. A correct password signs the browser in for
// SessionDuration and goes to the setup page.
func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	if a.Password == "" {
		return a.render(w, http.StatusServiceUnavailable, pages.Login{})
	}
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(a.Password)) != 1 {
		// Slow down guessing.
		time.Sleep(500 * time.Millisecond)
		return a.render(w, http.StatusUnauthorized, pages.Login{Configured: true, Error: "That password isn't right."})
	}
	if err := a.Session.Set(w, Session{ExpiresAt: time.Now().Add(SessionDuration)}); err != nil {
		return err
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
	return nil
}

// requireSession redirects to /login until the browser signs in.
func (a *App) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.signedIn(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// signedIn reports whether r carries a valid, unexpired session. Sessions
// are signed with a key derived from the password, so none are valid when
// it is empty.
func (a *App) signedIn(r *http.Request) bool {
	if a.Password == "" {
		return false
	}
	s, err := a.Session.Get(r)
	return err == nil && time.Now().Before(s.ExpiresAt)
}
