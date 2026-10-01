package api

import (
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

func init() {
	auth.HandleRawPrimary("GET /api/v1/server-info", handler.ServerInfo)
}

// serverInfoResponse advertises upload limits so clients can pick the right
// upload strategy BEFORE sending anything, instead of discovering a proxy's
type serverInfoResponse struct {
	// MaxDirectUploadBytes is the largest request body a client should send as
	MaxDirectUploadBytes int64 `json:"max_direct_upload_bytes"`
	// MaxUploadBytes is the server's cap on a single artifact's TOTAL size,
	MaxUploadBytes int64 `json:"max_upload_bytes"`
	// UploadSessions reports that chunked upload sessions are supported.
	UploadSessions bool `json:"upload_sessions"`
	// UploadBySHA256 reports that hash-reference uploads are supported: an
	UploadBySHA256 bool `json:"upload_by_sha256"`
}

// ServerInfo is public (like /healthz): clients need the limits to shape an
// upload before they have proven they may perform it.
func (h *Handler) ServerInfo(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, serverInfoResponse{
		MaxDirectUploadBytes: config.MaxDirectUploadSize(),
		MaxUploadBytes:       maxUploadSize,
		UploadSessions:       true,
		UploadBySHA256:       true,
	})
}
