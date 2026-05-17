package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = filepath.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

const maxUploadSize = 2 << 30 // 2 GiB

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

	if !t.AuthorizedForProject(project.ID) {
		jsonError(w, http.StatusForbidden, "token not authorized for this project")
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

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	hasher := sha256.New()
	body := io.TeeReader(r.Body, hasher)

	storageKey, size, err := h.Store.Put(r.Context(), body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to store artifact")
		return
	}

	sha256hex := hex.EncodeToString(hasher.Sum(nil))

	filename := sanitizeFilename(r.Header.Get("X-Artifact-Filename"))

	a := &model.Artifact{
		ReleaseID:  release.ID,
		OS:         model.OS(osStr),
		Arch:       model.Arch(archStr),
		Kind:       model.Kind(kind),
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256hex,
		Filename:   filename,
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
