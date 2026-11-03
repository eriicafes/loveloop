package pages

import (
	"net/http"

	"github.com/eriicafes/tmpl"
)

// Error explains why a page could not be shown.
type Error struct {
	Status  int
	Message string
}

func (p Error) Tmpl() tmpl.Template {
	return tmpl.Wrap(Layout{Title: p.StatusText() + " · LoveLoop"}, tmpl.Tmpl("pages/error", p))
}

// StatusText names Status, such as "Not Found".
func (p Error) StatusText() string {
	return http.StatusText(p.Status)
}
