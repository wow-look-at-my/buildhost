package apt

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
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
		hashes, err = h.computePackagesHashes(r, project, release)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
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
		hashes, err = h.computePackagesHashes(r, project, release)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
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

// computePackagesHashes renders each architecture's Packages index through
// packagesEntry -- the same renderer servePackages serves -- and hashes the
// rendered bytes, so the Release/InRelease SHA256 lines can never disagree
func (h *Handler) computePackagesHashes(r *http.Request, project *db.Project, release *db.Release) ([]hashEntry, error) {
	arches := []string{"amd64", "arm64", "i386", "armhf"}
	baseURL := auth.RequestRootURL(r)
	var entries []hashEntry

	for _, arch := range arches {
		data, err := h.packagesEntry(r.Context(), project, release, arch, baseURL)
		if err != nil {
			return nil, err
		}
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
	return entries, nil
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
