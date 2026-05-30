// Package llms serves /llms.txt, a public, unauthenticated document that
// describes buildhost and how to use it for LLMs and automated agents.
//
// See https://llmstxt.org for the convention. The content is rendered once
// from an embedded template with the server's configured base URL substituted
// in, so every example URL points at the live deployment.
package llms

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

//go:embed template.md
var templateMD string

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.body = render(auth.BaseURL())
	})
	// Public: no project, no auth. Registered raw so the project-auth
	// middleware never runs for this endpoint.
	auth.HandleRaw("GET /llms.txt", handler.Serve)
}

// Handler serves the pre-rendered /llms.txt body.
type Handler struct {
	body []byte
}

// render substitutes the deployment's base URL (and bare host) into the
// embedded template.
func render(baseURL string) []byte {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")

	out := strings.ReplaceAll(templateMD, "__BASE_URL__", base)
	out = strings.ReplaceAll(out, "__HOST__", host)
	return []byte(out)
}

// Serve writes the rendered /llms.txt document as plain text.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	body := h.body
	if body == nil {
		// Defensive: if OnReady has not run (e.g. handler used directly in a
		// test without auth.Init), render from the default base URL.
		body = render("")
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(body)
}
