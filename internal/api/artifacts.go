package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (h *Handler) UploadArtifact(w http.ResponseWriter, r *http.Request) {
	t := auth.TokenFrom(r.Context())
	if t == nil || !t.HasScope("write") {
		jsonError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	projectName := r.PathValue("project")
	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get project")
		return
	}

	version := r.PathValue("version")
	release, err := h.DB.GetRelease(r.Context(), project.ID, version)
	if errors.Is(err, db.ErrNotFound) {
		jsonError(w, http.StatusNotFound, "release not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to get release")
		return
	}

	if release.Published {
		jsonError(w, http.StatusConflict, "release already published")
		return
	}

	osStr := r.PathValue("os")
	archStr := r.PathValue("arch")
	if !model.ValidOS(osStr) {
		jsonError(w, http.StatusBadRequest, "invalid os")
		return
	}
	if !model.ValidArch(archStr) {
		jsonError(w, http.StatusBadRequest, "invalid arch")
		return
	}

	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = r.Header.Get("X-Artifact-Kind")
	}
	if kind == "" {
		kind = "binary"
	}
	if !model.ValidKind(kind) {
		jsonError(w, http.StatusBadRequest, "invalid kind")
		return
	}

	hasher := sha256.New()
	body := io.TeeReader(r.Body, hasher)

	storageKey, size, err := h.Store.Put(r.Context(), body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to store artifact")
		return
	}

	sha256hex := hex.EncodeToString(hasher.Sum(nil))

	a := &model.Artifact{
		ReleaseID:  release.ID,
		OS:         model.OS(osStr),
		Arch:       model.Arch(archStr),
		Kind:       model.Kind(kind),
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256hex,
		Filename:   r.Header.Get("X-Artifact-Filename"),
	}

	if err := h.DB.CreateArtifact(r.Context(), a); err != nil {
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, "artifact already exists for this os/arch")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}

	jsonResponse(w, http.StatusCreated, a)
}
