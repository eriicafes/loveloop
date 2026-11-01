// Package pages declares LoveLoop's pages. Each struct renders the html
// file of the same name.
package pages

import "github.com/eriicafes/tmpl"

// Layout is the document shell of every page.
type Layout struct {
	Title string
}

func (l Layout) Tmpl() tmpl.Template { return tmpl.Tmpl("pages/layout", l) }
