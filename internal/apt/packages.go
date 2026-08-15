package apt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

var validDebVersion = regexp.MustCompile(`^[a-zA-Z0-9.+~:-]+$`)

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

	entry, err := h.packagesEntry(r.Context(), project, release, arch, auth.RequestRootURL(r))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(entry))
}

// packagesEntry renders the Packages stanza for one architecture, or "" when
// the release exposes no apt package for it (no linux artifact for the arch, a
// docker-only artifact, or a version no deb can carry). It is the SINGLE
// renderer behind both the served Packages index (servePackages) and the
// Release/InRelease hash computation (computePackagesHashes), so the signed
// hashes always describe exactly the bytes the index route serves. The deb
// Size/SHA256 come from the packaged_artifacts digest cache (debDigest): one
// DB read once cached, a single repackage+hash on the first need.
func (h *Handler) packagesEntry(ctx context.Context, project *db.Project, release *db.Release, debArch, baseURL string) (string, error) {
	goArch := goArchFromDeb(debArch)
	artifact, err := h.DB.GetPlatformArtifact(ctx, release.ID, string(db.OSLinux), goArch)
	if errors.Is(err, db.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Docker images aren't debs: a docker-only release exposes no apt package.
	if artifact.Kind.ServedViaDockerOnly() {
		return "", nil
	}

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}
	if !validDebVersion.MatchString(version) {
		return "", nil
	}

	debSize, debSHA, err := h.debDigest(ctx, project, release, artifact, baseURL)
	if err != nil {
		return "", err
	}

	// The project name may be slash-namespaced; fold it to a valid Debian
	// package name (and matching pool filename). servePool resolves the project
	// from the request path, not this filename, so the rename is safe.
	pkgName := repackage.DebPackageName(project.Name)
	desc := strings.NewReplacer("\n", " ", "\r", " ").Replace(project.Description)
	return fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
Filename: pool/%s_%s_%s.deb
Size: %d
SHA256: %s
Description: %s

`, pkgName, version, debArch, pkgName, version, debArch,
		debSize, debSHA, desc), nil
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

	debArch := extractPoolArch(filename)
	if debArch == "" {
		http.NotFound(w, r)
		return
	}
	goArch := goArchFromDeb(debArch)

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}

	static.Redirect(w, r, auth.DeriveServiceURL(r, "static"), static.For(project.Name).WithVersion(version).WithOS(db.OSLinux).WithArch(db.Arch(goArch)).WithFmt("deb"), http.StatusFound)
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

func extractPoolArch(filename string) string {
	name := strings.TrimSuffix(filename, ".deb")
	if name == filename {
		return ""
	}
	i := strings.LastIndex(name, "_")
	if i < 0 {
		return ""
	}
	return name[i+1:]
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
