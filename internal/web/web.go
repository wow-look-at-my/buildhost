// Package web serves buildhost's public, read-only browse frontend on the main
// domain. It is intentionally NOT a single-page app: every page is rendered
// server-side as plain semantic HTML so the registry is consumable and
// indexable without executing any JavaScript. There is no JS at all -- styling
// lives in a single same-origin stylesheet.
//
// The frontend exposes only what the public REST API already exposes: public
// projects, their published releases, and the artifacts within them. Private
// projects are gated by the same auth.requireProject middleware used by every
// other read endpoint, so an anonymous visitor can never see a private project.
package web

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var templateFS embed.FS

// templates maps a page name to its parsed template. Each page is parsed
// together with the shared base layout; the page file redefines the "title"
// and "content" blocks that base.html renders.
var templates = map[string]*template.Template{
	"index":   parsePage("index.html"),
	"project": parsePage("project.html"),
	"release": parsePage("release.html"),
}

func parsePage(page string) *template.Template {
	return template.Must(template.New("base.html").
		Funcs(templateFuncs).
		ParseFS(templateFS, "templates/base.html", "templates/"+page))
}
