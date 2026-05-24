package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/static"
)

func init() {
	static.RegisterFmt(&npmWrapperFmt{})
}

type npmWrapperFmt struct{}

func (f *npmWrapperFmt) Name() string { return "npm-wrapper" }

func (f *npmWrapperFmt) Serve(w http.ResponseWriter, r *http.Request, ctx static.ServeContext) error {
	version := strings.TrimPrefix(ctx.Release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d.0.0", ctx.Release.VersionNum)
	}
	if !strings.Contains(version, ".") {
		version = version + ".0.0"
	}

	pkgJSON, _ := json.MarshalIndent(map[string]any{
		"name":    "@buildhost/" + ctx.Project.Name,
		"version": version,
	}, "", "  ")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := string(pkgJSON) + "\n"
	tw.WriteHeader(&tar.Header{
		Name: "package/package.json",
		Size: int64(len(content)),
		Mode: 0o644,
	})
	tw.Write([]byte(content))
	tw.Close()
	gw.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	io.Copy(w, &buf)
	return nil
}
