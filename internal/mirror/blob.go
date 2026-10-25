// fetchBlob streams a blob to the client and to a temporary file at once, so a large layer starts to flow at once.

package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
)

func (h *Handler) blob(w http.ResponseWriter, r *http.Request, repo, digest string) {
	ctx := r.Context()
	blob, found, err := h.stored(ctx, digest)
	if err != nil {
		h.fail(w, err, "BLOB_UNKNOWN")
		return
	}
	if found {
		h.serveStored(w, r, blob)
		return
	}
	if r.Method == http.MethodHead {
		h.headBlob(w, r, repo, digest)
		return
	}
	h.fetchBlob(w, r, repo, digest)
}

func (h *Handler) headBlob(w http.ResponseWriter, r *http.Request, repo, digest string) {
	resp, err := h.upstream(r.Context(), http.MethodHead, repo, "blobs/"+digest, "")
	if err != nil {
		h.fail(w, err, "BLOB_UNKNOWN")
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.fail(w, h.statusError(resp, repo, "blobs/"+digest), "BLOB_UNKNOWN")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
}

// fetchBlob streams a blob to the client and to a temporary file at once, so
// a large layer starts to flow at once. The file goes to the store only when
// its digest matches. A client that goes away does not stop the fetch, so the
// next pull still finds the blob.
func (h *Handler) fetchBlob(w http.ResponseWriter, r *http.Request, repo, digest string) {
	ctx := context.WithoutCancel(r.Context())
	resp, err := h.upstream(ctx, http.MethodGet, repo, "blobs/"+digest, "")
	if err != nil {
		h.fail(w, err, "BLOB_UNKNOWN")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.fail(w, h.statusError(resp, repo, "blobs/"+digest), "BLOB_UNKNOWN")
		return
	}
	if err := os.MkdirAll(h.TmpDir, 0o755); err != nil {
		h.fail(w, err, "BLOB_UNKNOWN")
		return
	}
	tmp, err := os.CreateTemp(h.TmpDir, "blob-*")
	if err != nil {
		h.fail(w, err, "BLOB_UNKNOWN")
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", digest)
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash, &clientWriter{w: w}), resp.Body)
	if err != nil {
		slog.Error("mirror: blob fetch broke off; nothing stored", "upstream", h.Up.Service, "repository", repo, "digest", digest, "bytes", n, "err", err)
		return
	}
	if got := "sha256:" + hex.EncodeToString(hash.Sum(nil)); got != digest {
		slog.Error("mirror: the upstream sent a blob that does not match its digest; nothing stored", "upstream", h.Up.Service, "repository", repo, "digest", digest, "got", got)
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		slog.Error("mirror: rewind blob", "digest", digest, "err", err)
		return
	}
	if _, err := h.save(ctx, digest, "", tmp); err != nil {
		slog.Error("mirror: store blob", "upstream", h.Up.Service, "digest", digest, "err", err)
		return
	}
	slog.Info("mirror: stored blob", "upstream", h.Up.Service, "repository", repo, "digest", digest, "bytes", n)
}

// clientWriter drops what the client can no longer take, so the copy to the
// temporary file goes on.
type clientWriter struct {
	w    io.Writer
	gone bool
}

func (c *clientWriter) Write(p []byte) (int, error) {
	if !c.gone {
		if _, err := c.w.Write(p); err != nil {
			c.gone = true
		}
	}
	return len(p), nil
}
