package apt

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (h *Handler) servePackages(w http.ResponseWriter, r *http.Request, projectName, subpath string) {
	arch := extractDebArch(subpath)
	if arch == "" {
		http.NotFound(w, r)
		return
	}

	project, err := h.DB.GetProject(r.Context(), projectName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

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
	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, string(model.OSLinux), goArch)
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

	desc := strings.NewReplacer("\n", " ", "\r", " ").Replace(project.Description)
	entry := fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
Filename: pool/%s_%s_%s.deb
Size: %d
SHA256: %s
Description: %s

`, projectName, version, arch, projectName, version, arch,
		artifact.Size, artifact.SHA256, desc)

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(entry))
}

func (h *Handler) servePool(w http.ResponseWriter, r *http.Request, projectName, subpath string) {
	filename := strings.TrimPrefix(subpath, "pool/")
	if filename == "" {
		http.NotFound(w, r)
		return
	}

	project, err := h.DB.GetProject(r.Context(), projectName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	artifacts, err := h.DB.ListArtifacts(r.Context(), release.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for _, a := range artifacts {
		if a.OS != model.OSLinux {
			continue
		}
		storageKey, size, _, _, err := h.DB.GetPackagedArtifact(r.Context(), a.ID, "deb")
		if err != nil {
			continue
		}
		rc, _, err := h.Store.Get(r.Context(), storageKey)
		if err != nil {
			continue
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/vnd.debian.binary-package")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		io.Copy(w, rc)
		return
	}

	http.NotFound(w, r)
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
