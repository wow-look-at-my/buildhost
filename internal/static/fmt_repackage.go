package static

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

type repackageFmt struct {
	format repackage.Format
}

func RegisterRepackageFmt(format repackage.Format) {
	RegisterFmt(&repackageFmt{format: format})
}

func (f *repackageFmt) Name() string { return string(f.format) }

func (f *repackageFmt) Serve(w http.ResponseWriter, r *http.Request, ctx ServeContext) error {
	if ctx.Artifact.StorageKey == "" {
		return fmt.Errorf("format %s requires an artifact", f.format)
	}

	rp, ok := repackage.LookupRepackager(f.format)
	if !ok {
		return fmt.Errorf("unsupported format: %s", f.format)
	}

	reader, size, err := repackage.OpenArtifactStream(r.Context(), ctx.Store, ctx.Artifact, ctx.TmpDir)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}

	out, err := rp.Repackage(r.Context(), repackage.Input{
		Project:  ctx.Project,
		Release:  ctx.Release,
		Artifact: ctx.Artifact,
		Reader:   reader,
		Size:     size,
		TmpDir:   ctx.TmpDir,
		BaseURL:  ctx.BaseURL,
		DownloadURL: func(name, version string, os db.OS, arch db.Arch, format string) string {
			return URL(ctx.StaticURL, For(name).WithVersion(version).WithOS(os).WithArch(arch).WithFmt(format))
		},
	})
	if err != nil {
		reader.Close()
		return err
	}
	// The repackager reads the input lazily, so the input must stay open until the
	// output is fully read. Closing out.Reader closes the pipe and then the input.
	out.Reader = repackage.ChainClose(out.Reader, reader)
	defer out.Reader.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", out.Filename))
	// A streamed (size-unknown) body is sent with chunked Transfer-Encoding; a known-size
	// output keeps Content-Length.
	if out.Size >= 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", out.Size))
	}
	if _, err := io.Copy(w, out.Reader); err != nil {
		// The (partial) body is already on the wire under chunked encoding; we can't turn
		// this into a clean HTTP error. Log and drop -- the truncated stream signals it.
		slog.Error("stream repackaged artifact", "format", f.format, "err", err)
	}
	return nil
}
