package apt

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

func (h *Handler) servePackages(w http.ResponseWriter, r *http.Request, subpath string) {
	arch := extractDebArch(subpath)
	if arch == "" {
		http.NotFound(w, r)
		return
	}

	project := auth.ProjectFrom(r.Context())

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	goArch := goArchFromDeb(arch)
	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, string(db.OSLinux), goArch)
	if errors.Is(err, db.ErrNotFound) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}

	debSize := artifact.Size
	debSHA := artifact.SHA256
	out, err := h.Gen.Generate(r.Context(), repackage.FormatDeb, *project, *release, *artifact)
	if err == nil {
		data, rerr := io.ReadAll(out.Reader)
		if rerr == nil {
			debSize = int64(len(data))
			h := sha256.Sum256(data)
			debSHA = fmt.Sprintf("%x", h)
		}
	}

	desc := strings.NewReplacer("\n", " ", "\r", " ").Replace(project.Description)
	entry := fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
Filename: pool/%s_%s_%s.deb
Size: %d
SHA256: %s
Description: %s

`, project.Name, version, arch, project.Name, version, arch,
		debSize, debSHA, desc)

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(entry))
}

func (h *Handler) servePool(w http.ResponseWriter, r *http.Request, subpath string) {
	filename := strings.TrimPrefix(subpath, "pool/")
	if filename == "" {
		http.NotFound(w, r)
		return
	}

	project := auth.ProjectFrom(r.Context())

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	debArch := extractDebArch(subpath)
	goArch := goArchFromDeb(debArch)

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}

	static.Redirect(w, r, h.BaseURL, static.For(project.Name).WithVersion(version).WithOS(db.OSLinux).WithArch(db.Arch(goArch)).WithFmt("deb"))
}

func extractDebArch(subpath string) string {
	if i := strings.Index(subpath, "binary-"); i >= 0 {
		rest := subpath[i+7:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

func goArchFromDeb(debArch string) string {
	switch debArch {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "i386":
		return "386"
	case "armhf":
		return "arm"
	default:
		return debArch
	}
}
