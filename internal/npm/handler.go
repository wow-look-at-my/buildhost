package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	// scope slash, so the path arrives as the single segment
	// `@buildhost%2f<name>`. Per RFC 3986 a percent-encoded slash is a literal
	// character, not a path separator, so the router keeps it in one segment
	// (and percent-decodes the captured value). Match the whole package segment
	// and strip the `@buildhost/` scope ourselves -- a `/@buildhost/{project}`
	// pattern would only match the rare unencoded client.
	auth.ServiceHandleHandler("npm", "GET /{pkg}", parseRoute, &handler)
	// Tarball URLs are emitted by us in the packument with literal slashes (npm
	// fetches dist.tarball verbatim, without scope-encoding), so they arrive as
	// a normal multi-segment path and need their own route.
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

// npm package names may contain at most one slash (the "@scope/name"
// separator), but buildhost projects are slash-namespaced to any depth
// (e.g. "cc-marketplace/my-plugin"). Encode the namespace separator as "__"
// so a namespaced project maps to a single valid npm package name and back.
// Project name segments must not themselves contain "__".
func projectToNPMName(project string) string { return strings.ReplaceAll(project, "/", "__") }
func npmNameToProject(name string) string    { return strings.ReplaceAll(name, "__", "/") }

func parseRoute(r *http.Request) auth.RouteInfo {
	// The router has already percent-decoded the segment, so both the encoded
	// (`@buildhost%2ffoo`) and unencoded (`@buildhost/foo`) forms arrive here as
	// `@buildhost/foo`. Anything without the scope is not a package request.
	name, ok := strings.CutPrefix(r.PathValue("pkg"), "@buildhost/")
	if !ok {
		return route{}
	}
	// Slash-namespaced projects are encoded with "__" in the npm name (see
	// projectToNPMName); decode before resolving the project.
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
	DB    *db.DB
	Store storage.Storage
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
	artifacts, err := h.DB.ListArtifacts(ctx, releaseID)
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

	versions := map[string]any{}
	distTags := map[string]string{}

	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		version := normalizeVersion(rel.Version)

		// Check for a pre-built npm-package artifact first.
		if npmPkg, err := h.DB.GetArtifactByKind(r.Context(), rel.ID, db.KindNPMPackage); err == nil && npmPkg != nil {
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
			// this the packument would advertise a package with no dependency
			// graph -- e.g. a launcher whose optionalDependencies are invisible
			// -- so npm would never install the sub-packages it needs and the
			// artifact would install but never work. name/version/dist stay
			// buildhost-authoritative and are not overridden.
			for k, v := range h.npmManifestFields(r.Context(), npmPkg.StorageKey) {
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
	// version number, so a feature-branch publish cannot hijack the npm latest.
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
