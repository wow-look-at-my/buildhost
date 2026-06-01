package static

import (
	"fmt"
	"io"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
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

	rc, _, err := ctx.Store.Get(r.Context(), ctx.Artifact.StorageKey)
	if err != nil {
		return fmt.Errorf("artifact not found")
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}

	if (ctx.Artifact.Kind == db.KindBinary || ctx.Artifact.Kind == db.KindLibrary) && strip.Available() {
		if result, err := strip.StripBytes(data, ctx.TmpDir); err == nil {
			data = result.Stripped
		}
	}

	rp, ok := repackage.LookupRepackager(f.format)
	if !ok {
		return fmt.Errorf("unsupported format: %s", f.format)
	}

	out, err := rp.Repackage(r.Context(), repackage.Input{
		Project:  ctx.Project,
		Release:  ctx.Release,
		Artifact: ctx.Artifact,
		Data:     data,
		BaseURL:  ctx.BaseURL,
		DownloadURL: func(name, version string, os db.OS, arch db.Arch, format string) string {
			return URL(ctx.StaticURL, For(name).WithVersion(version).WithOS(os).WithArch(arch).WithFmt(format))
		},
	})
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", out.Filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", out.Size))
	io.Copy(w, out.Reader)
	return nil
}
