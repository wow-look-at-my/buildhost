package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.BaseURL = auth.BaseURL()
	})
	auth.HandleHandler("/npm/", parseRoute, http.StripPrefix("/npm", &handler))
}

type route struct {
	project   string
	isTarball bool
	filename  string
}

func (r route) ProjectName() string     { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

func parseRoute(r *http.Request) auth.RouteInfo {
	path := strings.TrimPrefix(r.URL.Path, "/npm/")

	if strings.Contains(path, "/-/") {
		parts := strings.SplitN(path, "/-/", 2)
		packagePath := parts[0]
		filename := parts[1]
		projectName := strings.TrimPrefix(packagePath, "@buildhost/")
		mainProject := strings.SplitN(projectName, "-", 2)[0]
		return route{project: mainProject, isTarball: true, filename: filename}
	}

	projectName := strings.TrimPrefix(path, "@buildhost/")
	parts := strings.SplitN(projectName, "-", 2)
	if len(parts) > 0 {
		projectName = parts[0]
	}
	return route{project: projectName}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB      *db.DB
	Store   storage.Storage
	BaseURL string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	ri := routeFrom(r.Context())

	if ri.isTarball {
		h.serveTarball(w, r, project, ri.filename)
		return
	}

	h.servePackageInfo(w, r, project)
}

func (h *Handler) servePackageInfo(w http.ResponseWriter, r *http.Request, project *model.Project) {
	projectName := project.Name

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

func (h *Handler) serveTarball(w http.ResponseWriter, r *http.Request, project *model.Project, filename string) {
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
