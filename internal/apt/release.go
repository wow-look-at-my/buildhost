package apt

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

func (h *Handler) serveRelease(w http.ResponseWriter, r *http.Request, idx aptIndex, inRelease bool) {
	content, err := releaseContent(idx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if inRelease && h.Signer != nil && h.Signer.Available() {
		signed, err := h.Signer.ClearSign([]byte(content))
		if err != nil {
			http.Error(w, "signing failed", http.StatusInternalServerError)
			return
		}
		idx.headers(w, "text/plain")
		w.Write(signed)
		return
	}

	idx.headers(w, "text/plain")
	w.Write([]byte(content))
}

func (h *Handler) serveReleaseGPG(w http.ResponseWriter, r *http.Request, idx aptIndex) {
	if h.Signer == nil || !h.Signer.Available() {
		http.NotFound(w, r)
		return
	}

	content, err := releaseContent(idx)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	sig, err := h.Signer.DetachedSign([]byte(content))
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}

	idx.headers(w, "application/pgp-signature")
	w.Write(sig)
}

// releaseContent renders the Release file of an index over the Packages renderer servePackages serves.
func releaseContent(idx aptIndex) (string, error) {
	hashes, err := packagesHashes(idx)
	if err != nil {
		return "", err
	}
	return buildRelease(idx.label, hashes), nil
}

// serveKey serves the public signing key. A credentialed scope passes its own cacheControl, so a shared cache never keeps the answer.
func (h *Handler) serveKey(w http.ResponseWriter, r *http.Request, cacheControl string) {
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
	w.Header().Set("Cache-Control", cacheControl)
	w.Write(key)
}

// hashEntry is a SHA256 line of a Release file. releaseTemplate reads the
// fields, so they are exported.
type hashEntry struct {
	Path string
	Hash string
	Size int
}

// packagesHashes renders each architecture's Packages index through the
// index's renderer -- the one servePackages serves -- and hashes the rendered
// bytes. The Release/InRelease SHA256 lines can never disagree
func packagesHashes(idx aptIndex) ([]hashEntry, error) {
	arches := []string{"amd64", "arm64", "i386", "armhf"}
	var entries []hashEntry

	for _, arch := range arches {
		data, err := idx.packages(arch)
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
