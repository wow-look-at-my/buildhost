package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

var handler Handler

func init() {
	auth.OnReady(func() {
		handler.DB = auth.DB()
		handler.BaseURL = auth.BaseURL()
	})
	auth.HandleHandler("/npm/", parseRoute, http.StripPrefix("/npm", &handler))
}

type route struct {
	project  string
	platform string // e.g. "linux-x64", empty for base package
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
	packageName := strings.TrimPrefix(path, "@buildhost/")
	projectName, platform := splitPlatform(packageName)
	return route{project: projectName, platform: platform}
}

func routeFrom(ctx context.Context) route {
	return auth.RouteInfoFrom(ctx).(route)
}

type Handler struct {
	DB      *db.DB
	BaseURL string
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
			"bin":     map[string]string{projectName: "./bin/run.js"},
			"dist": map[string]string{
				"tarball": static.URL(h.BaseURL, static.For(projectName).WithVersion(version).WithOS("any").WithArch("any").WithFmt("npm-wrapper")),
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
				"tarball": static.URL(h.BaseURL, static.For(projectName).WithVersion(version).WithOS(model.OS(reverseNpmPlatform(platOS))).WithArch(model.Arch(reverseNpmArch(platArch))).WithFmt("npm")),
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

func wrapperRunScript(projectName string) string {
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
