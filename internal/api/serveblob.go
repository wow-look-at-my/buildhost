package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// serveCIBlob serves a stored blob that one CI job reads from another: a run
// file or a cache entry. It honors Range, so a download that breaks can resume,
// and names the blob's sha256 in X-Content-SHA256 for the client to check.
func (h *Handler) serveCIBlob(w http.ResponseWriter, r *http.Request, name, storageKey, what string) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-SHA256", storageKey)
	if rg, ok := h.Store.(storage.RandomGetter); ok {
		ra, size, err := rg.OpenReaderAt(ctx, storageKey)
		if err == nil {
			defer ra.Close()
			http.ServeContent(w, r, name, time.Time{}, io.NewSectionReader(ra, 0, size))
			return
		}
		if !errors.Is(err, storage.ErrRandomUnsupported) {
			slog.ErrorContext(ctx, what+": open", "key", storageKey, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to open "+what)
			return
		}
	}
	rc, size, err := h.Store.Get(ctx, storageKey)
	if err != nil {
		slog.ErrorContext(ctx, what+": open", "key", storageKey, "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to open "+what)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}

// storeCIBody stores a CI client's request body as sent. The client compresses
// it already. It writes the error and returns false.
func (h *Handler) storeCIBody(w http.ResponseWriter, r *http.Request, what string) (string, int64, bool) {
	ctx := r.Context()
	body := http.MaxBytesReader(w, r.Body, maxUploadSize)
	var storageKey string
	var size int64
	var err error
	if up, ok := h.Store.(storage.UncompressedPutter); ok {
		storageKey, size, err = up.PutUncompressed(ctx, body)
	} else {
		storageKey, size, err = h.Store.Put(ctx, body)
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonError(w, http.StatusRequestEntityTooLarge, what+" exceeds "+strconv.FormatInt(maxUploadSize, 10)+" bytes")
			return "", 0, false
		}
		slog.ErrorContext(ctx, what+": store", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to store "+what)
		return "", 0, false
	}
	return storageKey, size, true
}
