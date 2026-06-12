package static

import (
	"fmt"
	"io"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/strip"
)

type rawFmt struct{}

func (f *rawFmt) Name() string { return "raw" }

func (f *rawFmt) Serve(w http.ResponseWriter, r *http.Request, ctx ServeContext) error {
	if ctx.Artifact.StorageKey == "" {
		return fmt.Errorf("raw format requires an artifact")
	}

	debug := r.URL.Query().Get("debug") == "1"
	shouldStrip := strip.Available() && !debug

	if strip.Available() {
		w.Header().Set("X-Debug-Symbols", "available")
	} else {
		w.Header().Set("X-Debug-Symbols", "unavailable")
	}

	rc, size, err := ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}

	if shouldStrip {
		if sr, ssize, serr := strip.StripReader(rc, ctx.TmpDir); serr == nil {
			rc.Close()
			defer sr.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", ssize))
			io.Copy(w, sr)
			return nil
		}
		// strip failed (e.g. not an ELF): the reader was consumed, so re-open and serve
		// the artifact unstripped.
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
