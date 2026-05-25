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

	rc, size, err := ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}
	defer rc.Close()

	if strip.Available() {
		w.Header().Set("X-Debug-Symbols", "available")
	} else {
		w.Header().Set("X-Debug-Symbols", "unavailable")
	}

	if !shouldStrip {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		io.Copy(w, rc)
		return nil
	}

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}

	result, serr := strip.StripBytes(data, ctx.TmpDir)
	if serr == nil {
		data = result.Stripped
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
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
	defer rc.Close()

	// Must buffer: stripping requires full binary in memory.
	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}

	result, err := strip.StripBytes(data, ctx.TmpDir)
	if err != nil {
		return fmt.Errorf("no debug symbols")
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", ctx.Project.Name+".debug"))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(result.Debug)))
	w.Write(result.Debug)
	return nil
}
