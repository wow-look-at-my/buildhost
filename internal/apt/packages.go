package apt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

var validDebVersion = regexp.MustCompile(`^[a-zA-Z0-9.+~:-]+$`)

func (h *Handler) servePackages(w http.ResponseWriter, r *http.Request, idx aptIndex, subpath string) {
	arch := extractDebArch(subpath)
	if arch == "" {
		http.NotFound(w, r)
		return
	}

	entry, err := idx.packages(arch)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	idx.headers(w, "text/plain")
	w.Write([]byte(entry))
}

// aptIndex is one APT repository: the projects it lists, the Label of its
// Release, and the Cache-Control of its answers.
type aptIndex struct {
	label   string
	entries []indexEntry
	scoped  bool
	ctx     context.Context
	h       *Handler
	baseURL string
}

// indexEntry is one project of an index, at its latest release. pool is the
// directory of its debs, relative to the repository root.
type indexEntry struct {
	project *db.Project
	release *db.Release
	pool    string
}

// packages renders the Packages index of one architecture: the entry of each project, in order.
func (idx aptIndex) packages(debArch string) (string, error) {
	var b strings.Builder
	for _, e := range idx.entries {
		entry, err := idx.h.packagesEntry(idx.ctx, e.project, e.release, debArch, idx.baseURL, e.pool)
		if err != nil {
			return "", err
		}
		b.WriteString(entry)
	}
	return b.String(), nil
}

// headers sets the content type and the cache policy. A credentialed answer
// depends on the Authorization header, so no shared cache may keep it.
func (idx aptIndex) headers(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
	if idx.scoped {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Authorization")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}

// projectIndex is the repository of the request's project. A project with no release lists nothing.
func (h *Handler) projectIndex(r *http.Request) (aptIndex, error) {
	project := auth.ProjectFrom(r.Context())
	idx := aptIndex{label: project.Name, ctx: r.Context(), h: h, baseURL: auth.RequestRootURL(r)}
	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		return idx, nil
	}
	if err != nil {
		return idx, err
	}
	idx.entries = []indexEntry{{project: project, release: release, pool: "pool/"}}
	return idx, nil
}

func (h *Handler) packagesEntry(ctx context.Context, project *db.Project, release *db.Release, debArch, baseURL, pool string) (string, error) {
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
	pkgName := repackage.DebPackageName(project.Name)
	desc := strings.NewReplacer("\n", " ", "\r", " ").Replace(project.Description)
	dependsLine, err := repackage.DebDependsLine(*project)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
%sFilename: %s%s_%s_%s.deb
Size: %d
SHA256: %s
Description: %s

`, pkgName, version, debArch, dependsLine, pool, pkgName, version, debArch,
		debSize, debSHA, desc), nil
}

func (h *Handler) servePool(w http.ResponseWriter, r *http.Request, subpath string) {
	filename := strings.TrimPrefix(subpath, "pool/")
	if filename == "" {
		http.NotFound(w, r)
		return
	}
	h.poolRedirect(w, r, auth.ProjectFrom(r.Context()), filename, false)
}

// poolRedirect sends a pool download of project to the static endpoint. signed
// puts a short-lived download token in the Location. A client with a
// credential for the apt host only still gets the deb of a private project.
func (h *Handler) poolRedirect(w http.ResponseWriter, r *http.Request, project *db.Project, filename string, signed bool) {
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

	p := static.For(project.Name).WithVersion(version).WithOS(db.OSLinux).WithArch(db.Arch(goArch)).WithFmt("deb")
	if signed && project.IsPrivate {
		target, _ := static.SignedURL(auth.DeriveServiceURL(r, "static"), p, time.Now().Add(signedPoolTTL))
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	static.Redirect(w, r, auth.DeriveServiceURL(r, "static"), p, http.StatusFound)
}

// signedPoolTTL bounds the download token in a pool redirect of the authenticated source. apt follows the redirect at once.
const signedPoolTTL = 15 * time.Minute

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
