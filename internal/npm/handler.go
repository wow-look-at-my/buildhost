package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/static"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
	})
	// npm requests a scoped package as `@buildhost/<name>` but URL-encodes the
	auth.ServiceHandleHandler("npm", "GET /{pkg}", parseRoute, &handler)
	// Tarball URLs are emitted by us in the packument with literal slashes (npm
	auth.ServiceHandle("npm", "GET /@buildhost/{project}/-/{filename}", parseTarballRoute, handler.serveTarball)
}

type route struct {
	project  string
	platform string
}

func (r route) ProjectName() string      { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

type tarballRoute struct {
	project  string
	filename string
}

func (r tarballRoute) ProjectName() string      { return r.project }
func (r tarballRoute) Access() auth.AccessLevel { return auth.ReadAccess }

var knownPlatforms []string

func init() {
	for _, os := range []string{"linux", "darwin", "win32"} {
		for _, arch := range []string{"x64", "arm64", "ia32"} {
			knownPlatforms = append(knownPlatforms, os+"-"+arch)
		}
	}
}

func splitPlatform(name string) (project, platform string) {
	for _, p := range knownPlatforms {
		suffix := "-" + p
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix), p
		}
	}
	return name, ""
}

func projectToNPMName(project string) string { return strings.ReplaceAll(project, "/", "__") }
func npmNameToProject(name string) string    { return strings.ReplaceAll(name, "__", "/") }

func parseRoute(r *http.Request) auth.RouteInfo {
	// The router has already percent-decoded the segment, so both the encoded
	name, ok := strings.CutPrefix(r.PathValue("pkg"), "@buildhost/")
	if !ok {
		return route{}
	}
	// Slash-namespaced projects are encoded with "__" in the npm name (see
	projectName, platform := splitPlatform(npmNameToProject(name))
	return route{project: projectName, platform: platform}
}

func parseTarballRoute(r *http.Request) auth.RouteInfo {
	return tarballRoute{
		project:  npmNameToProject(r.PathValue("project")),
		filename: r.PathValue("filename"),
	}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

func tarballRouteFrom(ctx context.Context) tarballRoute {
	return auth.RouteInfoFrom(ctx).(tarballRoute)
}

type Handler struct {
	DB         *db.DB
	Store      storage.Storage
	fillBudget time.Duration
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	ri := routeFrom(r.Context())
	h.servePackageInfo(w, r, project, ri.platform)
}

type npmArtifactInfo struct {
	os   string
	arch string
}

func (h *Handler) collectNpmArtifacts(ctx context.Context, releaseID int64) []npmArtifactInfo {
	artifacts, err := h.DB.ListArtifactsByPlatform(ctx, releaseID)
	if err != nil {
		return nil
	}
	var infos []npmArtifactInfo
	for _, a := range artifacts {
		if a.Kind == db.KindLibrary || a.Kind.ServedViaDockerOnly() {
			continue
		}
		infos = append(infos, npmArtifactInfo{
			os:   npmPlatform(a.OS),
			arch: npmArch(a.Arch),
		})
	}
	return infos
}

func (h *Handler) servePackageInfo(w http.ResponseWriter, r *http.Request, project *db.Project, platform string) {
	projectName := project.Name
	npmName := projectToNPMName(projectName)

	releases, err := h.DB.ListReleases(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if platform != "" {
		h.servePlatformPackageInfo(w, r, project, platform, releases)
		return
	}

	type packumentRelease struct {
		release db.Release
		npmPkg  *db.Artifact
	}
	var (
		ordered []packumentRelease
		npmPkgs []db.Artifact
	)
	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		pr := packumentRelease{release: rel}
		if a, err := h.DB.GetArtifactByKind(r.Context(), rel.ID, db.KindNPMPackage); err == nil && a != nil {
			pr.npmPkg = a
			npmPkgs = append(npmPkgs, *a)
		}
		ordered = append(ordered, pr)
	}

	manifestFields, err := h.resolveNPMManifestFields(r.Context(), npmPkgs)
	if err != nil {
		slog.Warn("npm: packument manifest resolution", "project", projectName, "releases", len(npmPkgs), "err", err)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "npm package manifests are still being indexed, retry shortly", http.StatusServiceUnavailable)
		return
	}

	versions := map[string]any{}
	distTags := map[string]string{}

	for _, pr := range ordered {
		rel := pr.release
		version := normalizeVersion(rel.Version)

		// A pre-built npm-package artifact wins over binary repackaging.
		if pr.npmPkg != nil {
			npmBase := auth.DeriveServiceURL(r, "npm")
			tarballURL := fmt.Sprintf("%s/@buildhost/%s/-/%s-%s.tgz", npmBase, npmName, npmName, version)
			entry := map[string]any{
				"name":    "@buildhost/" + npmName,
				"version": version,
				"dist": map[string]string{
					"tarball": tarballURL,
				},
			}
			// Reflect the package's own manifest (dependencies, bin, os/cpu,
			// engines, ...) from the uploaded tarball's package.json. Without
			for k, v := range manifestFields[pr.npmPkg.ID] {
				if _, reserved := entry[k]; !reserved {
					entry[k] = v
				}
			}
			versions[version] = entry
			if _, ok := distTags["latest"]; !ok {
				distTags["latest"] = version
			}
			continue
		}

		// Fall back to binary repackaging.
		npmInfos := h.collectNpmArtifacts(r.Context(), rel.ID)

		optDeps := map[string]string{}
		for _, info := range npmInfos {
			platPkg := fmt.Sprintf("@buildhost/%s-%s-%s", npmName, info.os, info.arch)
			optDeps[platPkg] = version
		}

		versionEntry := map[string]any{
			"name":    "@buildhost/" + npmName,
			"version": version,
			"bin":     map[string]string{projectName: "./bin/run.js"},
			"dist": map[string]string{
				"tarball": static.URL(auth.DeriveServiceURL(r, "static"), static.For(projectName).WithVersion(version).WithOS("any").WithArch("any").WithFmt("npm-wrapper")),
			},
		}
		if len(optDeps) > 0 {
			versionEntry["optionalDependencies"] = optDeps
		}

		versions[version] = versionEntry
		if _, ok := distTags["latest"]; !ok {
			distTags["latest"] = version
		}
	}

	// Point "latest" at the default-branch (apex) release, not merely the highest
	if v := h.latestVersion(r.Context(), project); v != "" {
		if _, ok := versions[v]; ok {
			distTags["latest"] = v
		}
	}

	info := map[string]any{
		"name":      "@buildhost/" + npmName,
		"versions":  versions,
		"dist-tags": distTags,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(info)
}
