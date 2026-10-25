// Serve answers a raw download.

package static

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/wow-look-at-my/buildhost/internal/pack"
	"github.com/wow-look-at-my/buildhost/internal/recompress"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
)

type rawFmt struct{}

func (f *rawFmt) Name() string { return "raw" }

// Serve answers a raw download. The order is deliberate: a zstd client gets
// the stored stream untouched. A cached variant is served to anyone. A
// recompression nobody is allowed to start falls back to identity, and only
// then is work done.
func (f *rawFmt) Serve(w http.ResponseWriter, r *http.Request, ctx ServeContext) error {
	if ctx.Artifact.StorageKey == "" {
		return fmt.Errorf("raw format requires an artifact")
	}
	sh := ctx.Shaper
	debug := r.URL.Query().Get("debug") == "1"

	head, err := peekHead(r.Context(), ctx.Store, ctx.Artifact.StorageKey)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
	if strip.LooksELF(bytes.NewReader(head)) {
		w.Header().Set("X-Debug-Symbols", "available")
	} else {
		w.Header().Set("X-Debug-Symbols", "unavailable")
	}

	enc := Negotiate(r.Header.Get("Accept-Encoding"))

	// debug=1 is the upload, byte for byte, so nothing is transformed.
	if debug {
		return serveUpload(w, r, ctx, enc)
	}
	if enc == codingZstd && serveStoredZstd(w, r, ctx) {
		return nil
	}
	setCoding(w, enc)

	key := recompress.Key(ctx.Artifact.StorageKey, pack.TransformVersion, string(enc), debug)
	if serveCached(w, sh, key) {
		return nil
	}
	if enc != codingIdentity && !sh.recompressionAllowed(clientIP(r)) {
		enc = codingIdentity
		setCoding(w, enc)
		key = recompress.Key(ctx.Artifact.StorageKey, pack.TransformVersion, string(enc), debug)
		if serveCached(w, sh, key) {
			return nil
		}
	}

	p, err := sh.openPayload(r.Context(), ctx.Store, ctx.Artifact, ctx.TmpDir, head)
	if err != nil {
		return err
	}
	defer p.Close()

	// An untransformed stream straight from storage is the cheapest response
	// there is; it is also the common case for non-executable artifacts.
	if enc == codingIdentity && p.path == "" {
		return copyBody(w, p.rc, p.size)
	}

	// Identity of a transformed payload is already a file; only a coding needs a second pass.
	variant, size := p.path, p.size
	if enc != codingIdentity {
		variant, size, err = produce(sh, p, enc, ctx.TmpDir)
		if err != nil {
			slog.ErrorContext(r.Context(), "recompressing a download failed",
				"error", err, "encoding", string(enc), "storage_key", ctx.Artifact.StorageKey)
			return servePayload(w, p)
		}
		defer os.Remove(variant)
	}

	if sh != nil && sh.Cache != nil {
		if _, err := sh.Cache.Insert(key, variant); err == nil {
			if f, cached, ok := sh.Cache.Open(key); ok {
				defer f.Close()
				serveReader(w, f, cached)
				return nil
			}
		}
	}
	return servePath(w, variant, size)
}

// setCoding declares the response coding. Identity is the absence of the
// header, so a request that fell back from a compression clears it.
func setCoding(w http.ResponseWriter, enc coding) {
	if enc == codingIdentity {
		w.Header().Del("Content-Encoding")
		return
	}
	w.Header().Set("Content-Encoding", string(enc))
}

// serveUpload serves the stored bytes under the chosen coding. Only zstd is
// passed through; a debug request is never recompressed, so the body is the
// upload itself.
func serveUpload(w http.ResponseWriter, r *http.Request, ctx ServeContext, enc coding) error {
	if enc == codingZstd && serveStoredZstd(w, r, ctx) {
		return nil
	}
	p, err := streamStored(r.Context(), ctx.Store, ctx.Artifact.StorageKey)
	if err != nil {
		return err
	}
	defer p.Close()
	return serveReader(w, p.rc, p.size)
}

// serveStoredZstd streams the stored zstd blob unchanged. It reports false
// when the blob is stored uncompressed, in which case the caller produces a
// zstd variant.
func serveStoredZstd(w http.ResponseWriter, r *http.Request, ctx ServeContext) bool {
	cg, ok := ctx.Store.(storage.CompressedGetter)
	if !ok {
		return false
	}
	blob, err := cg.GetCompressed(r.Context(), ctx.Artifact.StorageKey)
	if err != nil {
		return false
	}
	if blob.Encoding != "zstd" {
		blob.Close()
		return false
	}
	defer blob.Close()
	w.Header().Set("Content-Encoding", "zstd")
	w.Header().Set("Content-Length", strconv.FormatInt(blob.Size, 10))
	_, err = io.Copy(w, blob)
	return true
}

// serveCached serves a variant from the cache. A cache hit performs no work
// and is therefore never throttled.
func serveCached(w http.ResponseWriter, sh *Shaper, key string) bool {
	if sh == nil || sh.Cache == nil {
		return false
	}
	f, size, ok := sh.Cache.Open(key)
	if !ok {
		return false
	}
	defer f.Close()
	// Headers are on the wire from the first byte; a copy error is the client's, and redoing the work would not help it.
	serveReader(w, f, size)
	return true
}

// produce writes the payload through the transfer coding into a temporary
// file, preferably one the cache can promote without a copy.
func produce(sh *Shaper, p *payload, enc coding, tmpDir string) (string, int64, error) {
	var (
		f   *os.File
		err error
	)
	if sh != nil && sh.Cache != nil {
		f, err = sh.Cache.Create()
	} else {
		f, err = os.CreateTemp(tmpDir, "compressed-*")
	}
	if err != nil {
		return "", 0, err
	}
	name := f.Name()
	fail := func(err error) (string, int64, error) {
		f.Close()
		os.Remove(name)
		return "", 0, err
	}
	var src io.Reader = p.rc
	if p.path != "" {
		sf, err := os.Open(p.path)
		if err != nil {
			return fail(err)
		}
		defer sf.Close()
		src = sf
	}
	if err := compress(enc, f, src); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", 0, err
	}
	size, err := fileSize(name)
	if err != nil {
		os.Remove(name)
		return "", 0, err
	}
	return name, size, nil
}

// servePayload serves the payload the request already holds, used when the
// requested coding could not be produced.
func servePayload(w http.ResponseWriter, p *payload) error {
	if p.path != "" {
		return servePath(w, p.path, p.size)
	}
	return copyBody(w, p.rc, p.size)
}

func servePath(w http.ResponseWriter, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return serveReader(w, f, size)
}

func serveReader(w http.ResponseWriter, rc io.Reader, size int64) error {
	return copyBody(w, rc, size)
}

func copyBody(w http.ResponseWriter, rc io.Reader, size int64) error {
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, err := io.Copy(w, rc)
	return err
}

type symbolsFmt struct{}

func (f *symbolsFmt) Name() string { return "symbols" }

func (f *symbolsFmt) Serve(w http.ResponseWriter, r *http.Request, ctx ServeContext) error {
	if ctx.Artifact.StorageKey == "" {
		return fmt.Errorf("symbols format requires an artifact")
	}

	if !strip.Available() {
		return fmt.Errorf("debug symbols not available")
	}

	rc, _, err := ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}

	dr, dsize, err := strip.StripReaderDebug(rc, ctx.TmpDir)
	rc.Close()
	if err != nil {
		return fmt.Errorf("no debug symbols")
	}
	defer dr.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name+".debug"))
	return copyBody(w, dr, dsize)
}
