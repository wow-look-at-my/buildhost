package apt

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/template"
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

// hashEntry is a SHA256 line of a Release file. releaseTemplate reads the
// fields, so they are exported.
type hashEntry struct {
	Path string
	Hash string
	Size int
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
			Path: fmt.Sprintf("main/binary-%s/Packages", arch),
			Hash: fmt.Sprintf("%x", hash),
			Size: len(data),
		})
	}
	return entries, nil
}

// releaseTemplate renders a Debian Release file. apt parses it as a field list,
// so a dropped newline merges adjacent fields and the index stops resolving.
var releaseTemplate = template.Must(template.New("apt-release").Parse(
	`Origin: buildhost
Label: {{.Project}}
Suite: stable
Codename: stable
Architectures: amd64 arm64 i386 armhf
Components: main
Date: {{.Date}}
{{if .Hashes}}SHA256:
{{range .Hashes}} {{.Hash}} {{.Size}} {{.Path}}
{{end}}{{end}}`))

func buildRelease(projectName string, hashes []hashEntry) string {
	var b strings.Builder
	err := releaseTemplate.Execute(&b, struct {
		Project string
		Date    string
		Hashes  []hashEntry
	}{
		Project: projectName,
		Date:    time.Now().UTC().Format(time.RFC1123Z),
		Hashes:  hashes,
	})
	if err != nil {
		panic(err) // Only an edit to releaseTemplate itself can reach this.
	}
	return b.String()
}
