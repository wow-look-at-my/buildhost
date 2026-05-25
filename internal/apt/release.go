package apt

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

func (h *Handler) serveRelease(w http.ResponseWriter, r *http.Request, inRelease bool) {
	project := auth.ProjectFrom(r.Context())

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		release = nil
	} else if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var hashes []hashEntry
	if release != nil {
		hashes = h.computePackagesHashes(r, project, release)
	}

	content := buildRelease(project.Name, hashes)

	if inRelease && h.Signer != nil && h.Signer.Available() {
		signed, err := h.Signer.ClearSign([]byte(content))
		if err != nil {
			http.Error(w, "signing failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(signed)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(content))
}

func (h *Handler) serveReleaseGPG(w http.ResponseWriter, r *http.Request) {
	if h.Signer == nil || !h.Signer.Available() {
		http.NotFound(w, r)
		return
	}

	project := auth.ProjectFrom(r.Context())

	release, err := h.DB.GetLatestRelease(r.Context(), project.ID)
	if errors.Is(err, db.ErrNotFound) {
		release = nil
	} else if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var hashes []hashEntry
	if release != nil {
		hashes = h.computePackagesHashes(r, project, release)
	}

	content := buildRelease(project.Name, hashes)

	sig, err := h.Signer.DetachedSign([]byte(content))
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pgp-signature")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(sig)
}

func (h *Handler) serveKey(w http.ResponseWriter, r *http.Request) {
	if h.Signer == nil || !h.Signer.Available() {
		http.NotFound(w, r)
		return
	}

	key, err := h.Signer.PublicKeyArmored()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pgp-keys")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(key)
}

type hashEntry struct {
	path string
	hash string
	size int
}

func (h *Handler) computePackagesHashes(r *http.Request, project *db.Project, release *db.Release) []hashEntry {
	arches := []string{"amd64", "arm64", "i386", "armhf"}
	var entries []hashEntry

	for _, arch := range arches {
		data := h.renderPackagesEntry(r, project, release, arch)
		if data == "" {
			continue
		}
		hash := sha256.Sum256([]byte(data))
		entries = append(entries, hashEntry{
			path: fmt.Sprintf("main/binary-%s/Packages", arch),
			hash: fmt.Sprintf("%x", hash),
			size: len(data),
		})
	}
	return entries
}

func (h *Handler) renderPackagesEntry(r *http.Request, project *db.Project, release *db.Release, debArch string) string {
	goArch := goArchFromDeb(debArch)
	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, string(db.OSLinux), goArch)
	if err != nil {
		return ""
	}

	version := strings.TrimPrefix(release.Version, "v")
	if version == "" {
		version = fmt.Sprintf("%d", release.VersionNum)
	}

	if !validDebVersion.MatchString(version) {
		return ""
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
	return fmt.Sprintf(`Package: %s
Version: %s
Architecture: %s
Filename: pool/%s_%s_%s.deb
Size: %d
SHA256: %s
Description: %s

`, project.Name, version, debArch, project.Name, version, debArch,
		debSize, debSHA, desc)
}

func buildRelease(projectName string, hashes []hashEntry) string {
	var b strings.Builder
	b.WriteString("Origin: buildhost\n")
	b.WriteString(fmt.Sprintf("Label: %s\n", projectName))
	b.WriteString("Suite: stable\n")
	b.WriteString("Codename: stable\n")
	b.WriteString("Architectures: amd64 arm64 i386 armhf\n")
	b.WriteString("Components: main\n")
	b.WriteString(fmt.Sprintf("Date: %s\n", time.Now().UTC().Format(time.RFC1123Z)))

	if len(hashes) > 0 {
		b.WriteString("SHA256:\n")
		for _, h := range hashes {
			b.WriteString(fmt.Sprintf(" %s %d %s\n", h.hash, h.size, h.path))
		}
	}

	return b.String()
}
