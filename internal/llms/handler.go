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
var serviceSubdomains = []string{"apt", "brew", "dl", "npm", "oci", "sites", "static"}

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

	return []byte(out)
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
