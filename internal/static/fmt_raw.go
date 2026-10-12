package static

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
)

type rawFmt struct{}

func (f *rawFmt) Name() string { return "raw" }

func (f *rawFmt) Serve(w http.ResponseWriter, r *http.Request, ctx ServeContext) error {
	if ctx.Artifact.StorageKey == "" {
		return fmt.Errorf("raw format requires an artifact")
	}

	debug := r.URL.Query().Get("debug") == "1"

	// Whether this artifact can be stripped is a property of the ARTIFACT, not
	strippable := false
	if peek, _, err := ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey); err == nil {
		strippable = strip.LooksELF(peek)
		peek.Close()
		if !strippable {
			strip.LogSkipped(r.Context(), ctx.Artifact.StorageKey, strip.ErrNotELF)
		}
	}
	shouldStrip := strippable && !debug

	// The header answers "can I fetch symbols for THIS artifact?", which is
	// only true when it is an ELF we can split. It used to report whether the
	if strippable {
		w.Header().Set("X-Debug-Symbols", "available")
	} else {
		w.Header().Set("X-Debug-Symbols", "unavailable")
	}

	// The raw artifact can be served either decompressed (identity) or, when the
	w.Header().Set("Vary", "Accept-Encoding")

	// zstd passthrough: when we are not stripping (stripping needs the real ELF
	// bytes) and the client accepts zstd, stream the stored zstd blob straight to
	// the client with Content-Encoding: zstd. buildhost never decompresses it --
	// the client bears that cost. Falls through to the normal decompressing path
	// when the store can't hand back compressed bytes or the blob is stored raw.
	if !shouldStrip && acceptsZstd(r.Header.Get("Accept-Encoding")) {
		if cg, ok := ctx.Store.(storage.CompressedGetter); ok {
			if blob, err := cg.GetCompressed(r.Context(), ctx.Artifact.StorageKey); err == nil {
				if blob.Encoding == "zstd" {
					defer blob.Close()
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
					w.Header().Set("Content-Encoding", "zstd")
					w.Header().Set("Content-Length", fmt.Sprintf("%d", blob.Size))
					io.Copy(w, blob)
					return nil
				}
				// Stored uncompressed: nothing to pass through; serve normally.
				blob.Close()
			}
		}
	}

	rc, size, err := ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}

	if shouldStrip {
		sr, ssize, serr := strip.StripReader(rc, ctx.TmpDir)
		if serr == nil {
			rc.Close()
			defer sr.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", ssize))
			io.Copy(w, sr)
			return nil
		}
		// Stripping failed on something that looked like an ELF: serve it
		strip.LogSkipped(r.Context(), ctx.Artifact.StorageKey, serr)
		rc.Close()
		rc, size, err = ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey)
		if err != nil {
			return fmt.Errorf("artifact not found")
		}
	}

	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	io.Copy(w, rc)
	return nil
}

// acceptsZstd reports whether an Accept-Encoding header lists zstd with a
func acceptsZstd(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "zstd") {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(p, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
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
	w.Header().Set("Content-Length", fmt.Sprintf("%d", dsize))
	io.Copy(w, dr)
	return nil
}
