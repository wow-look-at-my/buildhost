package npm

import (
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// The npm registry is served on the `npm.{domain}` subdomain, but clients
func init() {
	auth.HandleRaw("GET /npm/{path...}", redirectToNPMSubdomain)
	auth.HandleRaw("HEAD /npm/{path...}", redirectToNPMSubdomain)
}

func redirectToNPMSubdomain(w http.ResponseWriter, r *http.Request) {
	// Use the raw, still-escaped path. npm addresses a scoped package as a
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/npm")

	// Build the target host by prepending `npm.` to the apex host. Do NOT use
	host := hostWithoutPort(r.Host)
	if !strings.HasPrefix(host, "npm.") {
		host = "npm." + host
	}

	target := auth.RequestScheme(r) + "://" + host + rest
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	// http.Redirect writes the Location header verbatim (it only escapes
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// hostWithoutPort strips a trailing :port from a request Host, leaving the
func hostWithoutPort(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
