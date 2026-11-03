package web

import "net/http"

// Logout serves POST /logout.
func (a *App) Logout(w http.ResponseWriter, r *http.Request) error {
	a.Session.Delete(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
	return nil
}
