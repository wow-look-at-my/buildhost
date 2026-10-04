package npm

// Per-platform binary sub-packages: the platform packument, npm<->buildhost
// platform/arch name mapping, and the wrapper package's launcher script.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/static"
)

// latestVersion returns the version the npm "latest" dist-tag should point at:
// the apex (default-branch-aware) latest published release. Returns "" when none
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
