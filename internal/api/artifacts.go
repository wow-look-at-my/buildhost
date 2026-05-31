package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

var apiTracer = otel.Tracer("buildhost.api")

func init() {
	auth.Handle("PUT /api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}",
		parseRoute, handler.UploadArtifact)
}

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

// maxUploadSize caps a single REST artifact upload. It is read once from config
// (BUILDHOST_MAX_UPLOAD_SIZE) so the limit is tunable rather than hardcoded.
var maxUploadSize = config.MaxUploadSize()

func (h *Handler) UploadArtifact(w http.ResponseWriter, r *http.Request) {
	ctx, span := apiTracer.Start(r.Context(), "api.upload_artifact")
	defer span.End()

	project := auth.ProjectFrom(ctx)
	rt := routeFrom(ctx)

	span.SetAttributes(
		attribute.String("artifact.project", project.Name),
		attribute.String("artifact.version", rt.version),
		attribute.String("artifact.os", rt.os),
		attribute.String("artifact.arch", rt.arch),
	)

	release := h.getRelease(w, r, project.ID, rt.version)
	if release == nil {
		return
	}

	if release.Published {
		jsonError(w, http.StatusConflict, "release already published")
		return
	}

	if !db.ValidOS(rt.os) {
		jsonError(w, http.StatusBadRequest, "invalid os")
		return
	}
	if !db.ValidArch(rt.arch) {
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
	if !db.ValidKind(kind) {
		jsonError(w, http.StatusBadRequest, "invalid kind")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	hasher := sha256.New()
	body := io.TeeReader(r.Body, hasher)

	storageKey, size, err := h.Store.Put(ctx, body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "store failed")
		jsonError(w, http.StatusInternalServerError, "failed to store artifact")
		return
	}

	sha256hex := hex.EncodeToString(hasher.Sum(nil))
	span.SetAttributes(attribute.Int64("artifact.size", size))

	filename := sanitizeFilename(r.Header.Get("X-Artifact-Filename"))

	a := &db.Artifact{
		ReleaseID:  release.ID,
		OS:         db.OS(rt.os),
		Arch:       db.Arch(rt.arch),
		Kind:       db.Kind(kind),
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256hex,
		Filename:   filename,
	}

	if err := h.DB.CreateArtifact(ctx, a); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create artifact failed")
		if errors.Is(err, db.ErrConflict) {
			jsonError(w, http.StatusConflict, "artifact already exists for this os/arch")
			return
		}
		jsonError(w, http.StatusInternalServerError, "failed to record artifact")
		return
	}

	jsonResponse(w, http.StatusCreated, a)
}
