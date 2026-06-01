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
	auth.HandleRaw("GET /llms.txt", handler.Serve)
}

type Handler struct{}

func render(baseURL string) []byte {
	base := strings.TrimRight(baseURL, "/")
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")

	out := strings.ReplaceAll(templateMD, "__BASE_URL__", base)

	for _, svc := range []string{"apt", "brew", "dl", "npm", "oci", "sites", "static"} {
		placeholder := "__" + strings.ToUpper(svc) + "_URL__"
		out = strings.ReplaceAll(out, placeholder, base+"/"+svc)
	}
	out = strings.ReplaceAll(out, "__OCI_HOST__", host)

	return []byte(out)
}

// Serve renders the guide against this server's own base URL, derived from the
// request rather than a configured value.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(render(auth.RequestBaseURL(r)))
}
