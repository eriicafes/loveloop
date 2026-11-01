package web

import "net/http"

// logout serves POST /logout.
func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	a.Session.Delete(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
	return nil
}
