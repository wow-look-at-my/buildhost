package npm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Handler struct {
	DB      *db.DB
	Store   storage.Storage
	BaseURL string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	if strings.Contains(path, "/-/") {
		h.serveTarball(w, r, path)
		return
	}

	h.servePackageInfo(w, r, path)
}

func (h *Handler) servePackageInfo(w http.ResponseWriter, r *http.Request, packageName string) {
	projectName := strings.TrimPrefix(packageName, "@buildhost/")
	parts := strings.SplitN(projectName, "-", 2)
	if len(parts) > 0 {
		projectName = parts[0]
	}

	project, err := h.DB.GetProject(r.Context(), projectName)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if status, ok := auth.EnforceProjectRead(r, project); !ok {
		http.Error(w, http.StatusText(status), status)
		return
	}

	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	versions := map[string]any{}
	distTags := map[string]string{}

	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		version := strings.TrimPrefix(rel.Version, "v")
		if !strings.Contains(version, ".") {
			version = version + ".0.0"
		}

		versions[version] = map[string]any{
			"name":    "@buildhost/" + projectName,
			"version": version,
			"dist": map[string]string{
				"tarball": fmt.Sprintf("%s/npm/@buildhost/%s/-/%s-%s.tgz", h.BaseURL, projectName, projectName, version),
			},
		}
		if _, ok := distTags["latest"]; !ok {
			distTags["latest"] = version
		}
	}

	info := map[string]any{
		"name":      "@buildhost/" + projectName,
		"versions":  versions,
		"dist-tags": distTags,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (h *Handler) serveTarball(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(path, "/-/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}

	filename := parts[1]
	packagePath := parts[0]

	projectName := strings.TrimPrefix(packagePath, "@buildhost/")
	mainProject := strings.SplitN(projectName, "-", 2)[0]

	project, err := h.DB.GetProject(r.Context(), mainProject)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if status, ok := auth.EnforceProjectRead(r, project); !ok {
		http.Error(w, http.StatusText(status), status)
		return
	}

	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		artifacts, err := h.DB.ListArtifacts(r.Context(), rel.ID)
		if err != nil {
			continue
		}
		for _, a := range artifacts {
			storageKey, size, _, storedFilename, err := h.DB.GetPackagedArtifact(r.Context(), a.ID, "npm")
			if err != nil {
				continue
			}
			if storedFilename == filename || strings.HasSuffix(filename, storedFilename) {
				rc, _, err := h.Store.Get(r.Context(), storageKey)
				if err != nil {
					continue
				}
				defer rc.Close()
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
				io.Copy(w, rc)
				return
			}
		}
	}

	http.NotFound(w, r)
}
