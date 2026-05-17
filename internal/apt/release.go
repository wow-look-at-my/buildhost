package apt

import (
	"fmt"
	"net/http"
)

func (h *Handler) serveRelease(w http.ResponseWriter, _ *http.Request, projectName string) {
	content := fmt.Sprintf(`Origin: buildhost
Label: %s
Suite: stable
Codename: stable
Architectures: amd64 arm64 i386 armhf
Components: main
`, projectName)

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
}
