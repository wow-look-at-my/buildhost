package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// manifestPassthroughFields are the package.json fields buildhost surfaces from
// a pre-built npm-package tarball into its packument version entry. They are the
// fields npm needs to resolve and gate a package -- its dependency graph and its
// platform/engine constraints -- plus bin. name/version/dist stay
// buildhost-authoritative and are never taken from the tarball; lifecycle fields
// (scripts) are intentionally omitted so serving a packument never implies
// running install hooks.
var manifestPassthroughFields = []string{
	"dependencies",
	"optionalDependencies",
	"peerDependencies",
	"peerDependenciesMeta",
	"bundleDependencies",
	"bundledDependencies",
	"bin",
	"os",
	"cpu",
	"engines",
}

// npmManifestFields reads package/package.json from a stored npm-package tarball
// and returns the subset of fields (manifestPassthroughFields) buildhost echoes
// into the packument. Returns nil on any error -- missing blob, unreadable
// archive, bad JSON -- so the packument still serves a minimal-but-valid entry
// rather than failing the whole request.
func (h *Handler) npmManifestFields(ctx context.Context, storageKey string) map[string]any {
	rc, _, err := h.Store.Get(ctx, storageKey)
	if err != nil {
		return nil
	}
	defer rc.Close()

	pkg, err := readPackageJSONFromTarball(rc)
	if err != nil || pkg == nil {
		return nil
	}

	out := map[string]any{}
	for _, f := range manifestPassthroughFields {
		if v, ok := pkg[f]; ok {
			out[f] = v
		}
	}
	return out
}

// readPackageJSONFromTarball extracts and parses package/package.json from a
// gzipped npm tarball stream. The manifest read is capped to guard against a
// malicious or corrupt archive claiming a huge package.json.
func readPackageJSONFromTarball(r io.Reader) (map[string]any, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(hdr.Name, "./") != "package/package.json" {
			continue
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, io.LimitReader(tr, 1<<20)); err != nil {
			return nil, err
		}
		var pkg map[string]any
		if err := json.Unmarshal(buf.Bytes(), &pkg); err != nil {
			return nil, err
		}
		return pkg, nil
	}
}

func (h *Handler) serveTarball(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	ri := tarballRouteFrom(r.Context())
	filename := ri.filename

	// The tarball filename embeds the npm-encoded project name (see
	// projectToNPMName); decode happens at route parse, encode here to match.
	prefix := projectToNPMName(project.Name) + "-"
	if !strings.HasPrefix(filename, prefix) {
		http.NotFound(w, r)
		return
	}
	version := strings.TrimSuffix(filename[len(prefix):], ".tgz")
	if version == "" || version == filename[len(prefix):] {
		http.NotFound(w, r)
		return
	}

	release, err := h.DB.GetRelease(r.Context(), project.ID, version)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	artifact, err := h.DB.GetArtifactByKind(r.Context(), release.ID, db.KindNPMPackage)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rc, size, err := h.Store.Get(r.Context(), artifact.StorageKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprint(size))
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, rc)
}

// latestVersion returns the version the npm "latest" dist-tag should point at:
// the apex (default-branch-aware) latest published release. Returns "" when none
// resolves, so callers keep their highest-version fallback.
func (h *Handler) latestVersion(ctx context.Context, project *db.Project) string {
	rel, err := h.DB.GetLatestRelease(ctx, project.ID)
	if err != nil || rel == nil {
		return ""
	}
	return normalizeVersion(rel.Version)
}

func (h *Handler) servePlatformPackageInfo(w http.ResponseWriter, r *http.Request, project *db.Project, platform string, releases []db.Release) {
	projectName := project.Name
	platParts := strings.SplitN(platform, "-", 2)
	if len(platParts) != 2 {
		http.NotFound(w, r)
		return
	}
	platOS, platArch := platParts[0], platParts[1]
	packageName := projectToNPMName(projectName) + "-" + platform

	versions := map[string]any{}
	distTags := map[string]string{}

	for _, rel := range releases {
		if !rel.Published {
			continue
		}
		version := normalizeVersion(rel.Version)

		npmInfos := h.collectNpmArtifacts(r.Context(), rel.ID)
		found := false
		for _, info := range npmInfos {
			if info.os == platOS && info.arch == platArch {
				found = true
				break
			}
		}
		if !found {
			continue
		}

		versions[version] = map[string]any{
			"name":    "@buildhost/" + packageName,
			"version": version,
			"os":      []string{platOS},
			"cpu":     []string{platArch},
			"dist": map[string]string{
				"tarball": static.URL(auth.DeriveServiceURL(r, "static"), static.For(projectName).WithVersion(version).WithOS(db.OS(reverseNpmPlatform(platOS))).WithArch(db.Arch(reverseNpmArch(platArch))).WithFmt("npm")),
			},
		}
		if _, ok := distTags["latest"]; !ok {
			distTags["latest"] = version
		}
	}

	if len(versions) == 0 {
		http.NotFound(w, r)
		return
	}

	if v := h.latestVersion(r.Context(), project); v != "" {
		if _, ok := versions[v]; ok {
			distTags["latest"] = v
		}
	}

	info := map[string]any{
		"name":      "@buildhost/" + packageName,
		"versions":  versions,
		"dist-tags": distTags,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(info)
}

func reverseNpmPlatform(npm string) string {
	switch npm {
	case "win32":
		return "windows"
	default:
		return npm
	}
}

func reverseNpmArch(npm string) string {
	switch npm {
	case "x64":
		return "amd64"
	case "ia32":
		return "386"
	default:
		return npm
	}
}

func normalizeVersion(v string) string {
	version := strings.TrimPrefix(v, "v")
	if !strings.Contains(version, ".") {
		version = version + ".0.0"
	}
	return version
}

func npmPlatform(os db.OS) string {
	switch os {
	case db.OSDarwin:
		return "darwin"
	case db.OSWindows:
		return "win32"
	default:
		return string(os)
	}
}

func npmArch(a db.Arch) string {
	switch a {
	case db.ArchAMD64:
		return "x64"
	case db.ArchARM64:
		return "arm64"
	case db.Arch386:
		return "ia32"
	default:
		return string(a)
	}
}

func wrapperRunScript(projectName string) string {
	for _, c := range projectName {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return ""
		}
	}
	return `#!/usr/bin/env node
var pkg = "@buildhost/` + projectName + `-" + process.platform + "-" + process.arch;
var path = require("path");
var bin;
try { bin = path.join(path.dirname(require.resolve(pkg + "/package.json")), "bin", "` + projectName + `"); }
catch (e) { console.error("No binary package found for " + process.platform + "/" + process.arch + ". Install " + pkg); process.exit(1); }
var r = require("child_process").spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (r.error) throw r.error;
process.exitCode = r.status;
`
}
