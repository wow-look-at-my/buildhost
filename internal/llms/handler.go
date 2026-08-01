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

// serviceSubdomains are the service hosts buildhost dispatches by the first Host
// label. /llms.txt is served on each of them (in addition to the apex) so an
// agent landing on any buildhost host can discover the guide: the router's
// strict host partitioning means a known subdomain never falls through to the
// host-agnostic apex route, so a bare `GET /llms.txt` registration alone 404s on
// every subdomain. render() uses this same list to build the per-service URLs.
var serviceSubdomains = []string{"apt", "brew", "dl", "git", "npm", "oci", "sites", "static"}

func init() {
	auth.HandleRaw("GET /llms.txt", handler.Serve)
	for _, svc := range serviceSubdomains {
		auth.ServiceHandleRaw(svc, "GET /llms.txt", handler.Serve)
	}
}

type Handler struct{}

func render(baseURL string) []byte {
	base := strings.TrimRight(baseURL, "/")

	// Split scheme from host so service URLs can be built as subdomains. Each
	// service is dispatched by the first Host label (sites.{domain}, dl.{domain},
	// ...), so the public URL for a service is scheme://<svc>.<host>, matching
	// the server's own auth.DeriveServiceURL. Only the API stays on the main
	// domain.
	scheme := "https://"
	host := base
	if i := strings.Index(base, "://"); i >= 0 {
		scheme = base[:i+3]
		host = base[i+3:]
	}

	out := strings.ReplaceAll(templateMD, "__BASE_URL__", base)

	for _, svc := range serviceSubdomains {
		placeholder := "__" + strings.ToUpper(svc) + "_URL__"
		out = strings.ReplaceAll(out, placeholder, scheme+svc+"."+host)
	}
	out = strings.ReplaceAll(out, "__OCI_HOST__", "oci."+host)
	// The authenticated Homebrew tap URL carries the token as the HTTP Basic
	// password inside the URL (git only transmits credentials after a
	// challenge, and brew stores the remote verbatim). "$TOKEN" is a literal
	// shell placeholder for the reader, not a server-side substitution.
	out = strings.ReplaceAll(out, "__BREW_TOKEN_URL__", scheme+"x:$TOKEN@brew."+host)
	out = strings.ReplaceAll(out, "__SITE_SECTION__", siteSection(scheme))

	return []byte(out)
}

// siteSection documents the {project}.<site-domain> serving scheme when a site
// domain is configured, and renders nothing otherwise -- the served guide only
// describes endpoints this deployment actually has. The section's URLs live on
// the dedicated site domain, never on a service subdomain of the apex, so the
// per-subdomain rendering guards are unaffected.
func siteSection(scheme string) string {
	sd := auth.SiteDomain()
	if sd == "" {
		return ""
	}
	return `
## Project site subdomains

This deployment also serves each project's static site on a dedicated domain,
one subdomain per project (only for project names that are a valid single DNS
label: [a-z0-9-], max 63 chars, no leading/trailing hyphen):

` + "```" + `
` + scheme + `myapp.` + sd + `/                # default branch (same branch "latest" tracks)
` + scheme + `myapp.` + sd + `/@pr-7/          # any other branch, behind the @ sigil
` + scheme + `myapp.` + sd + `/@claude/foo/    # slash-named branches resolve by longest match
` + "```" + `

An @<default-branch> URL 302s to the canonical bare form. The path prefixes
"@" and "~" (at the path root) and "/__sso" are reserved on this scheme; other
project names and paths remain available on the classic sites URL above. "~"
was this scheme's original branch sigil and 301s to the "@" spelling.
`
}

// apexBaseURL returns the request's scheme + apex host. /llms.txt is served on
// the apex and on every service subdomain, but the guide's service URLs must
// always anchor to the apex (dl.<apex>, oci.<apex>, ...) -- so when the request
// arrived on a known service subdomain its leading label is stripped. This
// mirrors how the server itself dispatches by the first Host label, so a request
// on the apex (or any non-service host, including a bare IP in tests) is returned
// unchanged and never double-prefixed into dl.oci.<apex>.
func apexBaseURL(r *http.Request) string {
	host, port := r.Host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i:]
	}
	if dot := strings.IndexByte(host, '.'); dot > 0 && isServiceSubdomain(host[:dot]) {
		host = host[dot+1:]
	}
	return auth.RequestScheme(r) + "://" + host + port
}

func isServiceSubdomain(label string) bool {
	for _, svc := range serviceSubdomains {
		if label == svc {
			return true
		}
	}
	return false
}

// Serve renders the guide against this server's own apex base URL, derived from
// the request rather than a configured value.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(render(apexBaseURL(r)))
}
