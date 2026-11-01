package pages

import "github.com/eriicafes/tmpl"

// Login asks for the setup password.
type Login struct {
	// Configured reports whether SETUP_PASSWORD is set.
	Configured bool
	Error      string
}

func (p Login) Tmpl() tmpl.Template {
	return tmpl.Wrap(Layout{Title: "Sign in · LoveLoop"}, tmpl.Tmpl("pages/login", p))
}
