package apt

import (
	"fmt"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// TODO: sign Release/InRelease with GPG and include SHA256 hashes of Packages files
func (h *Handler) serveRelease(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	content := fmt.Sprintf(`Origin: buildhost
Label: %s
Suite: stable
Codename: stable
Architectures: amd64 arm64 i386 armhf
Components: main
`, project.Name)

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write([]byte(content))
}
