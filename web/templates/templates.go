// Package templates holds LoveLoop's page templates and components. Each
// page's struct sits beside its html file in pages. Everything in components
// is autoloaded into every page: the shadcn-style ui kit in components/ui and
// container components, such as the setup page's cards, beside it. Pages
// lay them out and wire them together.
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
