package npm

import (
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// The npm registry is served on the `npm.{domain}` subdomain, but clients
// (notably the go-toolchain GitHub Action) install the pre-built binary with an
// apex registry base:
//
//	npm install -g @buildhost/go-toolchain --registry=https://<apex>/npm/
//
// Bridge the two by 301-redirecting `/npm/<rest>` on the apex (main) domain to
// `npm.<apex>/<rest>` -- the npm `/npm` prefix is stripped. This mirrors the
// existing docker.{domain} -> oci.{domain} redirect (see oci/handler.go), except
// that one is subdomain->subdomain with an identical path while this one is
// apex-path->subdomain WITH a prefix strip, so it needs its own handler rather
// than auth.ServiceRedirect.
//
// Registered as a main-domain (host-agnostic) route: it fires only on the apex
// host. A request arriving on the npm subdomain is "claimed" by the npm service
// routes and never falls through here (router host partitioning).
func init() {
	auth.HandleRaw("GET /npm/{path...}", redirectToNPMSubdomain)
	auth.HandleRaw("HEAD /npm/{path...}", redirectToNPMSubdomain)
}

func redirectToNPMSubdomain(w http.ResponseWriter, r *http.Request) {
	// Use the raw, still-escaped path. npm addresses a scoped package as a
	// single percent-encoded segment (`@buildhost%2fgo-toolchain`), and the npm
	// `GET /{pkg}` route depends on that `%2f` STAYING encoded so the scoped
	// package remains one segment (see the init() comment in handler.go).
	// EscapedPath() returns URL.RawPath verbatim, so `%2f` survives; the target
	// is then assembled by string concatenation. Round-tripping through
	// url.URL{Path: ...}.String() would re-encode `%2f` back to a literal `/`
	// and break the {pkg} match, so we must not do that.
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/npm")

	// Build the target host by prepending `npm.` to the apex host. Do NOT use
	// auth.DeriveServiceURL / domainFromRequest here: those strip the first host
	// label (correct when a request arrives on a subdomain), so on the apex host
	// `pazer.build` they would yield `build` and target the wrong host
	// `npm.build`. The request reaches this handler on the apex, so the npm
	// subdomain is simply `npm.` + the full apex host.
	host := hostWithoutPort(r.Host)
	if !strings.HasPrefix(host, "npm.") {
		host = "npm." + host
	}

	target := auth.RequestScheme(r) + "://" + host + rest
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	// http.Redirect writes the Location header verbatim (it only escapes
	// non-ASCII bytes), so the pre-built, already-escaped target string is
	// emitted as-is and `%2f` stays encoded.
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// hostWithoutPort strips a trailing :port from a request Host, leaving the
// hostname. Mirrors the host handling used elsewhere in the server; production
// hosts are DNS names, so the naive last-colon split is sufficient.
func hostWithoutPort(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}
