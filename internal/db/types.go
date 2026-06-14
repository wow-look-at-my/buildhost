package db

import (
	"strings"
	"time"
)

type Versioning string

const (
	VersioningAuto   Versioning = "auto"
	VersioningSemver Versioning = "semver"
)

type OS string

const (
	OSLinux   OS = "linux"
	OSDarwin  OS = "darwin"
	OSWindows OS = "windows"
	OSFreeBSD OS = "freebsd"
)

type Arch string

const (
	ArchAMD64 Arch = "amd64"
	ArchARM64 Arch = "arm64"
	Arch386   Arch = "386"
	ArchARM   Arch = "arm"
)

type Kind string

const (
	KindBinary     Kind = "binary"
	KindLibrary    Kind = "library"
	KindAssets     Kind = "assets"
	KindArchive    Kind = "archive"
	KindDocker     Kind = "docker"
	KindNPMPackage Kind = "npm-package"
)

// ServedViaDockerOnly reports whether artifacts of this kind are exclusively
// served through the OCI (/v2) endpoint. A "docker build" is just a container
// image: it has no bare binary to repackage, so apt/brew/npm/raw downloads do
// not apply to it.
func (k Kind) ServedViaDockerOnly() bool {
	return k == KindDocker
}

func ValidOS(s string) bool {
	switch OS(s) {
	case OSLinux, OSDarwin, OSWindows, OSFreeBSD:
		return true
	}
	return false
}

func ValidArch(s string) bool {
	switch Arch(s) {
	case ArchAMD64, ArchARM64, Arch386, ArchARM:
		return true
	}
	return false
}

// NormalizeOS maps an operating-system name to its canonical db.OS, accepting the
// spellings GitHub Actions' RUNNER_OS uses ("Linux", "macOS", "Windows") and
// other common aliases so clients can pass platform names through verbatim. It
// returns ("", false) for an unrecognized name; callers should leave such a value
// untouched (e.g. the "any" sentinel) rather than rejecting it.
func NormalizeOS(s string) (OS, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "linux":
		return OSLinux, true
	case "darwin", "macos", "mac", "osx", "os x", "apple-darwin":
		return OSDarwin, true
	case "windows", "win", "win32", "win64":
		return OSWindows, true
	case "freebsd":
		return OSFreeBSD, true
	}
	return "", false
}

// NormalizeArch maps a CPU-architecture name to its canonical db.Arch, accepting
// GitHub Actions' RUNNER_ARCH spellings ("X64", "ARM64", "X86", "ARM"), uname's
// ("x86_64", "aarch64", "i686", ...), and other common aliases. It returns
// ("", false) for an unrecognized name; callers should leave such a value
// untouched (e.g. the "any" sentinel) rather than rejecting it.
func NormalizeArch(s string) (Arch, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "amd64", "x64", "x86_64", "x86-64", "x8664":
		return ArchAMD64, true
	case "arm64", "aarch64", "armv8", "arm64e":
		return ArchARM64, true
	case "386", "x86", "i386", "i686":
		return Arch386, true
	case "arm", "armv7", "armv7l", "armv6", "armv6l", "armhf":
		return ArchARM, true
	}
	return "", false
}

func ValidKind(s string) bool {
	switch Kind(s) {
	case KindBinary, KindLibrary, KindAssets, KindArchive, KindDocker, KindNPMPackage:
		return true
	}
	return false
}

type APIToken = ApiToken
type OIDCPolicy = OidcPolicy

type DashboardStats = GetDashboardStatsRow
type RecentRelease = ListRecentReleasesRow
type ProjectSummary = ListProjectSummariesRow
type ReleaseSummary = ListReleaseSummariesRow
type TokenDetail = ListTokenDetailsRow
type OIDCPolicyDetail = ListOIDCPolicyDetailsRow
type SiteDetail = ListSiteDetailsRow
type AllArtifact = ListAllArtifactsRow
type StorageBreakdown = GetStorageBreakdownRow

var ValidScopes = map[string]bool{
	"read":  true,
	"write": true,
	// share authorizes minting temporary, artifact-bound download links
	// (POST /api/v1/projects/{project}/download-links). It is deliberately
	// separate from write so a CI/deploy token cannot also hand out shareable
	// links to private artifacts.
	"share": true,
}

func (r Release) IsPrerelease() bool {
	return strings.Contains(r.Version, "-")
}

func (t ApiToken) HasScope(scope string) bool {
	for _, s := range splitScopes(t.Scopes) {
		if s == scope {
			return true
		}
	}
	return false
}

func (t ApiToken) IsExpired() bool {
	return t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now())
}

func (t ApiToken) IsGlobal() bool {
	return t.ProjectID == nil
}

func (t ApiToken) AuthorizedForProject(projectID int64) bool {
	return t.ProjectID == nil || *t.ProjectID == projectID
}

func (r ListTokenDetailsRow) IsExpired() bool {
	return r.ExpiresAt != nil && r.ExpiresAt.Before(time.Now())
}

func (r ListTokenDetailsRow) IsGlobal() bool {
	return r.ProjectID == nil
}

func splitScopes(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
