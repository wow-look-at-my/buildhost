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
	// OSWasm is the platform identifier for WebAssembly artifacts. The Arch
	// distinguishes the flavor -- Go's two wasm ports are ArchJS (GOOS=js,
	// the browser/Node port) and ArchWasip1 (GOOS=wasip1, WASI). OSWasm
	// pairs only with those arches (and vice versa); see CompatiblePlatform.
	OSWasm OS = "wasm"
)

type Arch string

const (
	ArchAMD64 Arch = "amd64"
	ArchARM64 Arch = "arm64"
	Arch386   Arch = "386"
	ArchARM   Arch = "arm"
	// ArchJS and ArchWasip1 are the WebAssembly flavors, named after Go's
	// GOOS for the port (js = browser/Node, wasip1 = WASI preview 1) so the
	// publisher mapping is trivial: os is always wasm, arch is the GOOS.
	// They only ever pair with OSWasm.
	ArchJS     Arch = "js"
	ArchWasip1 Arch = "wasip1"
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
	case OSLinux, OSDarwin, OSWindows, OSFreeBSD, OSWasm:
		return true
	}
	return false
}

func ValidArch(s string) bool {
	switch Arch(s) {
	case ArchAMD64, ArchARM64, Arch386, ArchARM, ArchJS, ArchWasip1:
		return true
	}
	return false
}

// wasmFlavorArch reports whether arch is one of the WebAssembly flavor
// architectures, which only ever pair with OSWasm.
func wasmFlavorArch(a Arch) bool {
	return a == ArchJS || a == ArchWasip1
}

// CompatiblePlatform reports whether the (os, arch) pair names a coherent
// platform: OSWasm pairs only with the wasm flavor arches (ArchJS/ArchWasip1)
// and those arches pair only with OSWasm -- a "linux/js" or "wasm/amd64"
// artifact could never be downloaded by anything real. Every other pairing is
// allowed (this is a coherence check, not a validity check; callers validate
// the individual values separately).
func CompatiblePlatform(os OS, arch Arch) bool {
	if os == OSWasm || wasmFlavorArch(arch) {
		return os == OSWasm && wasmFlavorArch(arch)
	}
	return true
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
	case "wasm", "webassembly":
		return OSWasm, true
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
	case "js":
		return ArchJS, true
	// Deliberately no bare "wasi" alias: WASI has versioned snapshots
	// (preview 1 today, preview 2 in the wings) and an unversioned alias
	// would silently change meaning later.
	case "wasip1":
		return ArchWasip1, true
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
