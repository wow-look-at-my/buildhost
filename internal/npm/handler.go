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
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.Store = auth.Store()
		handler.BaseURL = auth.BaseURL()
		handler.Gen = repackage.NewGenerator(auth.Store(), auth.DB(), auth.BaseURL())
	})
	auth.HandleHandler("/npm/", parseRoute, http.StripPrefix("/npm", &handler))
}

type route struct {
	project   string
	platform  string // e.g. "linux-x64", metadata routes only
	isTarball bool
	version   string // tarball routes only
	os        string // tarball routes only, e.g. "linux"
	arch      string // tarball routes only, e.g. "x64"
}

func (r route) ProjectName() string     { return r.project }
func (r route) Access() auth.AccessLevel { return auth.ReadAccess }

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

func parseRoute(r *http.Request) auth.RouteInfo {
	path := strings.TrimPrefix(r.URL.Path, "/npm/")

	if path == "pkg" || path == "pkg/" {
		q := r.URL.Query()
		name := strings.TrimPrefix(q.Get("name"), "@buildhost/")
		return route{
			project:   name,
			isTarball: true,
			version:   q.Get("v"),
			os:        q.Get("os"),
			arch:      q.Get("arch"),
		}
	}

	packageName := strings.TrimPrefix(path, "@buildhost/")
	projectName, platform := splitPlatform(packageName)
	return route{project: projectName, platform: platform}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB      *db.DB
	Store   storage.Storage
	BaseURL string
	Gen     *repackage.Generator
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	project := auth.ProjectFrom(r.Context())
	ri := routeFrom(r.Context())

	if ri.isTarball {
		h.serveTarball(w, r, project, ri)
		return
	}

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
		if a.Kind == model.KindLibrary {
			continue
		}
		infos = append(infos, npmArtifactInfo{
			os:   npmPlatform(a.OS),
			arch: npmArch(a.Arch),
		})
	}
	return infos
}

func (h *Handler) servePackageInfo(w http.ResponseWriter, r *http.Request, project *model.Project, platform string) {
	projectName := project.Name

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

		npmInfos := h.collectNpmArtifacts(r.Context(), rel.ID)

		optDeps := map[string]string{}
		for _, info := range npmInfos {
			platPkg := fmt.Sprintf("@buildhost/%s-%s-%s", projectName, info.os, info.arch)
			optDeps[platPkg] = version
		}

		versionEntry := map[string]any{
			"name":    "@buildhost/" + projectName,
			"version": version,
			"dist": map[string]string{
				"tarball": fmt.Sprintf("%s/npm/pkg?name=@buildhost/%s&v=%s", h.BaseURL, projectName, version),
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

	info := map[string]any{
		"name":      "@buildhost/" + projectName,
		"versions":  versions,
		"dist-tags": distTags,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(info)
}

func (h *Handler) servePlatformPackageInfo(w http.ResponseWriter, r *http.Request, project *model.Project, platform string, releases []model.Release) {
	projectName := project.Name
	platParts := strings.SplitN(platform, "-", 2)
	if len(platParts) != 2 {
		http.NotFound(w, r)
		return
	}
	platOS, platArch := platParts[0], platParts[1]
	packageName := projectName + "-" + platform

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
				"tarball": fmt.Sprintf("%s/npm/pkg?name=@buildhost/%s&v=%s&os=%s&arch=%s", h.BaseURL, projectName, version, platOS, platArch),
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

	info := map[string]any{
		"name":      "@buildhost/" + packageName,
		"versions":  versions,
		"dist-tags": distTags,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(info)
}

func (h *Handler) serveTarball(w http.ResponseWriter, r *http.Request, project *model.Project, ri route) {
	if ri.version == "" {
		http.Error(w, "missing version", http.StatusBadRequest)
		return
	}

	release := h.findReleaseByNpmVersion(r.Context(), project.ID, ri.version)
	if release == nil || !release.Published {
		http.NotFound(w, r)
		return
	}

	version := normalizeVersion(release.Version)

	if ri.os == "" {
		h.serveWrapperTarball(w, project.Name, version)
		return
	}

	artifact, err := h.DB.GetArtifact(r.Context(), release.ID, reverseNpmPlatform(ri.os), reverseNpmArch(ri.arch))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if artifact.Kind == model.KindLibrary {
		http.NotFound(w, r)
		return
	}

	out, err := h.Gen.Generate(r.Context(), repackage.FormatNPM, *project, *release, *artifact)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", out.Size))
	io.Copy(w, out.Reader)
}

func (h *Handler) findReleaseByNpmVersion(ctx context.Context, projectID int64, npmVersion string) *model.Release {
	rel, err := h.DB.GetRelease(ctx, projectID, npmVersion)
	if err == nil {
		return rel
	}
	if strings.HasSuffix(npmVersion, ".0.0") {
		rel, err = h.DB.GetRelease(ctx, projectID, strings.TrimSuffix(npmVersion, ".0.0"))
		if err == nil {
			return rel
		}
	}
	return nil
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

func (h *Handler) serveWrapperTarball(w http.ResponseWriter, projectName, version string) {
	pkgJSON, _ := json.MarshalIndent(map[string]any{
		"name":    "@buildhost/" + projectName,
		"version": version,
	}, "", "  ")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	content := string(pkgJSON) + "\n"
	tw.WriteHeader(&tar.Header{
		Name: "package/package.json",
		Size: int64(len(content)),
		Mode: 0o644,
	})
	tw.Write([]byte(content))
	tw.Close()
	gw.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	io.Copy(w, &buf)
}

func normalizeVersion(v string) string {
	version := strings.TrimPrefix(v, "v")
	if !strings.Contains(version, ".") {
		version = version + ".0.0"
	}
	return version
}

func npmPlatform(os model.OS) string {
	switch os {
	case model.OSDarwin:
		return "darwin"
	case model.OSWindows:
		return "win32"
	default:
		return string(os)
	}
}

func npmArch(a model.Arch) string {
	switch a {
	case model.ArchAMD64:
		return "x64"
	case model.ArchARM64:
		return "arm64"
	case model.Arch386:
		return "ia32"
	default:
		return string(a)
	}
}
