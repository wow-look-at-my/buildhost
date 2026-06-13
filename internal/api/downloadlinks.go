package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

func init() {
	auth.OnReady(func() {
		auth.HandleRaw("POST /api/v1/projects/{project}/download-links", handler.CreateDownloadLink)
	})
}

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

// CreateDownloadLink mints a temporary, artifact-bound, signed download URL. The
// returned link carries a &token= that authorizes exactly the requested
// (os, arch, fmt, version) -- nothing else in the project -- until it expires
// (default 1h, max 24h), so a private artifact can be shared without handing out
// a project token. Requires a token holding the "share" scope and authorized for
// the project. This route does its own auth (HandleRaw) because the gate is the
// share scope, not the read/write scopes requireProject knows about.
func (h *Handler) CreateDownloadLink(w http.ResponseWriter, r *http.Request) {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("share") {
		jsonError(w, http.StatusUnauthorized, "share scope required")
		return
	}

	project, err := h.DB.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to look up project")
		return
	}
	if !t.AuthorizedForProject(project.ID) {
		jsonError(w, http.StatusForbidden, "token not authorized for this project")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	var req createDownloadLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OS == "" || req.Arch == "" || req.Version == "" {
		jsonError(w, http.StatusBadRequest, "os, arch and version are required")
		return
	}
	if req.OS == "any" || req.Arch == "any" {
		jsonError(w, http.StatusBadRequest, `os and arch must be concrete (not "any")`)
		return
	}
	fmtStr := req.Fmt
	if fmtStr == "" {
		fmtStr = "raw"
	}
	if _, ok := static.LookupFmt(fmtStr); !ok {
		jsonError(w, http.StatusBadRequest, "unsupported format: "+fmtStr)
		return
	}

	rel := h.getRelease(w, r, project.ID, req.Version)
	if rel == nil {
		return
	}
	artifact, err := h.DB.GetArtifact(r.Context(), rel.ID, req.OS, req.Arch)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "no artifact for this os/arch")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to look up artifact")
		return
	}
	if artifact.Kind.ServedViaDockerOnly() {
		jsonError(w, http.StatusBadRequest, "docker images are downloaded via the OCI endpoint")
		return
	}

	exp := time.Now().Add(clampDownloadLinkTTL(req.TTLSeconds))
	resolvedVersion := resolvedDownloadVersion(rel)

	p := static.For(project.Name).
		WithVersion(resolvedVersion).
		WithOS(db.OS(req.OS)).
		WithArch(db.Arch(req.Arch)).
		WithFmt(fmtStr).
		WithDebug(req.Debug)
	urlStr, tok := static.SignedURL(auth.ApexServiceURL(r, "static"), p, exp)

	jsonResponse(w, http.StatusCreated, map[string]any{
		"url":        urlStr,
		"token":      tok,
		"expires_at": exp.UTC(),
	})
}

// clampDownloadLinkTTL maps a requested TTL (seconds; 0 = use default) into the
// allowed [min, max] window.
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

// resolvedDownloadVersion mirrors the dl handler: the static endpoint keys on a
// bare numeric/semver string (no leading "v"), falling back to the auto-increment
// number. Signing this exact string keeps the link's v= matching the signature.
func resolvedDownloadVersion(rel *db.Release) string {
	v := strings.TrimPrefix(rel.Version, "v")
	if v == "" {
		return fmt.Sprintf("%d", rel.VersionNum)
	}
	return v
}
