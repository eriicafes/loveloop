// Package templates holds LoveLoop's page templates and ui components. Each
// page's struct sits beside its html file in pages, and the components in
// components/ui are autoloaded into every page.
package templates

import (
	"embed"

	"github.com/eriicafes/tmpl"
	"github.com/eriicafes/tmpl/vite"
)

//go:embed pages components
var files embed.FS

// Parse parses the pages with the vite template funcs of v.
func Parse(v *vite.Vite) (*tmpl.Templates, error) {
	return tmpl.New(files).
		Funcs(v.Funcs()).
		Autoload("components").
		LoadTree("pages").
		Parse()
}
