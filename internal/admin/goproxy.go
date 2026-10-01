package admin

import (
	"log/slog"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/goproxy"
)

// apiGoproxy reports the Go module proxy's state.
//
// The dashboard exists because of how the previous proxy failed: with no
func (s *Server) apiGoproxy(w http.ResponseWriter, r *http.Request) {
	svc := goproxy.Current()
	if svc == nil {
		s.writeJSON(w, map[string]any{"enabled": false})
		return
	}

	state, err := svc.Snapshot(r.Context())
	if err != nil {
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{
		"enabled": true,
		"state":   state,
	})
}

// apiGoproxyRecheck re-runs the readiness probe on demand. An operator who has
// just fixed the credential should not have to wait out the poll interval to
// find out whether it worked.
func (s *Server) apiGoproxyRecheck(w http.ResponseWriter, r *http.Request) {
	svc := goproxy.Current()
	if svc == nil {
		http.Error(w, "goproxy is not running", http.StatusServiceUnavailable)
		return
	}
	s.writeJSON(w, svc.CheckHealthNow(r.Context()))
}
