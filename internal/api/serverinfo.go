package api

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

func init() {
	auth.OnReady(func() {
		auth.HandleRaw("GET /api/v1/server-info", handler.ServerInfo)
	})
}

// serverInfoResponse advertises upload limits so clients can pick the right
// upload strategy BEFORE sending anything, instead of discovering a proxy's
// request-body cap by watching a large upload die with an edge 413.
type serverInfoResponse struct {
	// MaxDirectUploadBytes is the largest request body a client should send as
	// one direct upload. Anything larger should go through a chunked upload
	// session (POST /api/v1/uploads). This reflects the proxy in front of the
	// server (e.g. Cloudflare's ~100 MB edge cap), not the server's own limit.
	MaxDirectUploadBytes int64 `json:"max_direct_upload_bytes"`
	// MaxUploadBytes is the server's cap on a single artifact's TOTAL size,
	// however it is delivered (direct or assembled from chunks).
	MaxUploadBytes int64 `json:"max_upload_bytes"`
	// UploadSessions reports that chunked upload sessions are supported.
	UploadSessions bool `json:"upload_sessions"`
}

// ServerInfo is public (like /healthz): clients need the limits to shape an
// upload before they have proven they may perform it.
func (h *Handler) ServerInfo(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, serverInfoResponse{
		MaxDirectUploadBytes: config.MaxDirectUploadSize(),
		MaxUploadBytes:       maxUploadSize,
		UploadSessions:       true,
	})
}
