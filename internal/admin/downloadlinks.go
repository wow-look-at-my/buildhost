package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

const (
	defaultDownloadLinkTTL = time.Hour
	maxDownloadLinkTTL     = 24 * time.Hour
	minDownloadLinkTTL     = time.Minute
)

type createDownloadLinkRequest struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Fmt        string `json:"fmt"`
	Version    string `json:"version"`
	Debug      bool   `json:"debug"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// apiCreateDownloadLink mints a temporary, artifact-bound, signed download URL
// for the dashboard's "copy temporary link" button. The admin dashboard sits
// behind a reverse proxy with access control, so this endpoint trusts the caller
// (no buildhost token / "share" scope is required) -- unlike the public REST
// endpoint. The returned link works for exactly one artifact until it expires.
func (s *Server) apiCreateDownloadLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, err := s.db.GetProject(ctx, r.PathValue("name"))
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var req createDownloadLinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.OS == "" || req.Arch == "" || req.Version == "" {
		http.Error(w, "os, arch and version are required", http.StatusBadRequest)
		return
	}
	if req.OS == "any" || req.Arch == "any" {
		http.Error(w, `os and arch must be concrete (not "any")`, http.StatusBadRequest)
		return
	}
	fmtStr := req.Fmt
	if fmtStr == "" {
		fmtStr = "raw"
	}
	if _, ok := static.LookupFmt(fmtStr); !ok {
		http.Error(w, "unsupported format", http.StatusBadRequest)
		return
	}

	rel, err := s.db.GetRelease(ctx, project.ID, req.Version)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	artifact, err := s.db.GetArtifact(ctx, rel.ID, req.OS, req.Arch)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("admin api error", "err", err, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if artifact.Kind.ServedViaDockerOnly() {
		http.Error(w, "docker images are downloaded via the OCI endpoint", http.StatusBadRequest)
		return
	}

	exp := time.Now().Add(clampDownloadLinkTTL(req.TTLSeconds))
	v := strings.TrimPrefix(rel.Version, "v")
	if v == "" {
		v = fmt.Sprintf("%d", rel.VersionNum)
	}

	p := static.For(project.Name).
		WithVersion(v).
		WithOS(db.OS(req.OS)).
		WithArch(db.Arch(req.Arch)).
		WithFmt(fmtStr).
		WithDebug(req.Debug)
	urlStr, tok := static.SignedURL(auth.ApexServiceURL(r, "static"), p, exp)

	s.writeJSON(w, map[string]any{
		"url":        urlStr,
		"token":      tok,
		"expires_at": exp.UTC(),
	})
}

func clampDownloadLinkTTL(seconds int64) time.Duration {
	ttl := defaultDownloadLinkTTL
	if seconds > 0 {
		ttl = time.Duration(seconds) * time.Second
	}
	if ttl < minDownloadLinkTTL {
		return minDownloadLinkTTL
	}
	if ttl > maxDownloadLinkTTL {
		return maxDownloadLinkTTL
	}
	return ttl
}
