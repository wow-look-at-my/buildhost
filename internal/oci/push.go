package oci

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// contentRangePattern matches the OCI distribution chunk range form
// "<start>-<end>" (inclusive byte offsets, no "bytes " prefix).
var contentRangePattern = regexp.MustCompile(`^([0-9]+)-([0-9]+)$`)

// StartBlobUpload handles POST /v2/{name}/blobs/uploads/.
//
// Two modes:
//   - monolithic: ?digest=sha256:... with the blob as the body -> store now, 201.
//   - session:    no digest -> open an upload session, 202 + Location for PATCH/PUT.
//
// A ?mount= request (cross-repo blob mount) is treated as a session start: we
// don't implement mounting, and the client falls back to a normal upload.
func (h *Handler) StartBlobUpload(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())

	digest := r.URL.Query().Get("digest")
	if digest != "" {
		if !validDigest.MatchString(digest) {
			ociError(w, http.StatusBadRequest, "DIGEST_INVALID", "invalid digest format")
			return
		}
		sess, err := h.uploads.start()
		if err != nil {
			ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to start upload")
			return
		}
		if _, err := h.uploads.appendChunk(sess, r.Body); err != nil {
			h.uploads.remove(sess)
			h.uploadError(w, err)
			return
		}
		stored, size, err := h.uploads.finalize(r.Context(), h.Store, sess, digest)
		if err != nil {
			h.uploadError(w, err)
			return
		}
		if err := h.DB.LinkOCIBlob(r.Context(), project.ID, stored[7:], "", size, false); err != nil {
			ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to record blob")
			return
		}
		w.Header().Set("Location", blobPath(project.Name, stored))
		w.Header().Set("Docker-Content-Digest", stored)
		w.WriteHeader(http.StatusCreated)
		return
	}

	sess, err := h.uploads.start()
	if err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to start upload")
		return
	}
	w.Header().Set("Location", uploadPath(project.Name, sess.uuid))
	w.Header().Set("Docker-Upload-UUID", sess.uuid)
	w.Header().Set("Range", "0-0")
	w.WriteHeader(http.StatusAccepted)
}

// PatchBlobUpload handles PATCH /v2/{name}/blobs/uploads/{uuid} (chunk append).
//
// A Content-Range header ("<start>-<end>", as the OCI distribution spec has
// chunked clients send) is verified against the bytes committed so far: a
// mismatched start returns 416 with the current Range and consumes nothing, so
// a client that lost a response can query where the server actually is and
// resume instead of corrupting the blob. Requests without the header keep the
// old append-only behavior (docker's single in-session PATCH sends none).
func (h *Handler) PatchBlobUpload(w http.ResponseWriter, r *http.Request, uuid string) {
	project := auth.ProjectFrom(r.Context())
	sess := h.uploads.get(uuid)
	if sess == nil {
		ociError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}

	start := int64(-1)
	if cr := r.Header.Get("Content-Range"); cr != "" {
		m := contentRangePattern.FindStringSubmatch(cr)
		if m == nil {
			ociError(w, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "malformed Content-Range")
			return
		}
		start, _ = strconv.ParseInt(m[1], 10, 64)
	}

	committed, err := h.uploads.appendChunkAt(sess, r.Body, start)
	if errors.Is(err, errRangeMismatch) {
		setUploadHeaders(w, project.Name, sess.uuid, committed)
		ociError(w, http.StatusRequestedRangeNotSatisfiable, "RANGE_INVALID",
			fmt.Sprintf("chunk start %d does not match committed size %d", start, committed))
		return
	}
	if err != nil {
		h.uploads.remove(sess)
		h.uploadError(w, err)
		return
	}
	setUploadHeaders(w, project.Name, sess.uuid, committed)
	w.WriteHeader(http.StatusAccepted)
}

// GetBlobUploadStatus handles GET /v2/{name}/blobs/uploads/{uuid}: the upload
// status read a chunked client uses to learn the committed size and resume
// after a lost response or connection.
func (h *Handler) GetBlobUploadStatus(w http.ResponseWriter, r *http.Request, uuid string) {
	project := auth.ProjectFrom(r.Context())
	sess := h.uploads.get(uuid)
	if sess == nil {
		ociError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}
	setUploadHeaders(w, project.Name, sess.uuid, h.uploads.committed(sess))
	w.WriteHeader(http.StatusNoContent)
}

// setUploadHeaders writes the shared upload-session response headers. Range is
// the inclusive byte range received so far, with the end clamped to >= 0 so an
// empty session reports "0-0" rather than an invalid "0--1".
func setUploadHeaders(w http.ResponseWriter, projectName, uuid string, committed int64) {
	end := committed - 1
	if end < 0 {
		end = 0
	}
	w.Header().Set("Location", uploadPath(projectName, uuid))
	w.Header().Set("Docker-Upload-UUID", uuid)
	w.Header().Set("Range", fmt.Sprintf("0-%d", end))
}

// PutBlobUpload handles PUT /v2/{name}/blobs/uploads/{uuid}?digest=... (finalize).
func (h *Handler) PutBlobUpload(w http.ResponseWriter, r *http.Request, uuid string) {
	project := auth.ProjectFrom(r.Context())
	sess := h.uploads.get(uuid)
	if sess == nil {
		ociError(w, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}
	digest := r.URL.Query().Get("digest")
	if !validDigest.MatchString(digest) {
		h.uploads.remove(sess)
		ociError(w, http.StatusBadRequest, "DIGEST_INVALID", "digest required to complete upload")
		return
	}
	if r.Body != nil {
		if _, err := h.uploads.appendChunk(sess, r.Body); err != nil {
			h.uploads.remove(sess)
			h.uploadError(w, err)
			return
		}
	}
	stored, size, err := h.uploads.finalize(r.Context(), h.Store, sess, digest)
	if err != nil {
		h.uploadError(w, err)
		return
	}
	if err := h.DB.LinkOCIBlob(r.Context(), project.ID, stored[7:], "", size, false); err != nil {
		ociError(w, http.StatusInternalServerError, "UNKNOWN", "failed to record blob")
		return
	}
	w.Header().Set("Location", blobPath(project.Name, stored))
	w.Header().Set("Docker-Content-Digest", stored)
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) uploadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errBlobTooLarge) {
		ociError(w, http.StatusRequestEntityTooLarge, "BLOB_UPLOAD_INVALID", "blob exceeds maximum size")
		return
	}
	ociError(w, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", err.Error())
}

func blobPath(name, digest string) string { return "/v2/" + name + "/blobs/" + digest }
func uploadPath(name, uuid string) string { return "/v2/" + name + "/blobs/uploads/" + uuid }
