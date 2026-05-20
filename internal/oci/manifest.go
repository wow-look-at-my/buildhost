package oci

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

var validDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (h *Handler) serveManifest(w http.ResponseWriter, r *http.Request, reference string) {
	project := auth.ProjectFrom(r.Context())
	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for _, a := range artifacts {
		storageKey, _, _, _, err := h.DB.GetPackagedArtifact(r.Context(), a.ID, "oci")
		if err != nil {
			continue
		}
		rc, size, err := h.Store.Get(r.Context(), storageKey)
		if err != nil {
			continue
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		io.Copy(w, rc)
		rc.Close()
		return
	}

	http.NotFound(w, r)
}

func (h *Handler) serveBlob(w http.ResponseWriter, r *http.Request, digest string) {
	if !validDigest.MatchString(digest) {
		http.NotFound(w, r)
		return
	}
	key := digest[7:]

	project := auth.ProjectFrom(r.Context())
	belongs, err := h.DB.BlobBelongsToProject(r.Context(), project.ID, key)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !belongs {
		http.NotFound(w, r)
		return
	}

	rc, size, err := h.Store.Get(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	w.Header().Set("Docker-Content-Digest", digest)
	io.Copy(w, rc)
}
