package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-containers/set"
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
	OSWasm OS = "wasm"
)

type Arch string

const (
	ArchAMD64 Arch = "amd64"
	ArchARM64 Arch = "arm64"
	Arch386   Arch = "386"
	ArchARM   Arch = "arm"
	// ArchJS and ArchWasip1 are the WebAssembly flavors, named after Go's
	ArchJS     Arch = "js"
	ArchWasip1 Arch = "wasip1"
)

type Platform struct {
	OS   OS   `json:"os"`
	Arch Arch `json:"arch"`
}

func (p Platform) String() string { return string(p.OS) + "/" + string(p.Arch) }

func ParsePlatform(s string) (Platform, error) {
	osPart, archPart, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok {
		return Platform{}, fmt.Errorf("invalid platform %q: want os/arch", strings.TrimSpace(s))
	}
	osName, ok := NormalizeOS(osPart)
	if !ok {
		return Platform{}, fmt.Errorf("invalid os %q in platform %q", strings.TrimSpace(osPart), strings.TrimSpace(s))
	}
	arch, ok := NormalizeArch(archPart)
	if !ok {
		return Platform{}, fmt.Errorf("invalid arch %q in platform %q", strings.TrimSpace(archPart), strings.TrimSpace(s))
	}
	if !CompatiblePlatform(osName, arch) {
		return Platform{}, fmt.Errorf("incompatible platform %q: os=wasm pairs only with arch js or wasip1 (and those arches only with os=wasm)", strings.TrimSpace(s))
	}
	return Platform{OS: osName, Arch: arch}, nil
}

// ParsePlatformList reads a comma-separated "os/arch,os/arch" set. An empty
// list and a duplicate after normalization ("macos/arm64,darwin/arm64") are
// both errors: a set that silently loses an entry would publish a binary as
// covering less than the publisher declared.
func ParsePlatformList(spec string) ([]Platform, error) {
	elems := strings.Split(spec, ",")
	out := make([]Platform, 0, len(elems))
	seen := set.New[Platform](len(elems))
	for _, elem := range elems {
		if strings.TrimSpace(elem) == "" {
			return nil, fmt.Errorf("empty platform in %q", spec)
		}
		p, err := ParsePlatform(elem)
		if err != nil {
			return nil, err
		}
		if !seen.Add(p) {
			return nil, fmt.Errorf("duplicate platform %q", p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no platforms")
	}
	return out, nil
}

// FormatPlatforms renders a set as "linux/amd64, darwin/arm64" for a badge or
// an error message.
func FormatPlatforms(platforms []Platform) string {
	parts := make([]string, len(platforms))
	for i, p := range platforms {
		parts[i] = p.String()
	}
	return strings.Join(parts, ", ")
}

// ArtifactWithPlatforms is an artifact plus every platform it covers. Platforms
type ArtifactWithPlatforms struct {
	Artifact
	Platforms []Platform `json:"platforms"`
}

func (a ArtifactWithPlatforms) MultiPlatform() bool { return len(a.Platforms) > 1 }

type PlatformArtifact struct {
	Artifact
	// CacheSuffix distinguishes this platform's derived packages from the same
	CacheSuffix string
}

// CacheFormat is the packaged_artifacts format key for a derived package of
func (p PlatformArtifact) CacheFormat(format string) string { return format + p.CacheSuffix }

func newPlatformArtifact(a Artifact, p Platform) PlatformArtifact {
	out := PlatformArtifact{Artifact: a}
	if a.OS != p.OS || a.Arch != p.Arch {
		out.OS, out.Arch = p.OS, p.Arch
		out.CacheSuffix = "@" + p.String()
	}
	return out
}

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

func wasmFlavorArch(a Arch) bool {
	return a == ArchJS || a == ArchWasip1
}

// CompatiblePlatform reports whether the (os, arch) pair names a coherent
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
	case "wasip1":
		return ArchWasip1, true
	}
	return "", false
}

// NormalizeLegacyWasmPair maps the deprecated GOOS/GOARCH-ordered
// WebAssembly pair -- (os=js, arch=wasm) or (os=wasip1, arch=wasm), the
// `name_GOOS_GOARCH` filename convention currently-released go-toolchain
// autoreleases derive upload parameters from -- to the canonical
func NormalizeLegacyWasmPair(osName, arch string) (OS, Arch, bool) {
	if strings.ToLower(strings.TrimSpace(arch)) != "wasm" {
		return "", "", false
	}
	switch strings.ToLower(strings.TrimSpace(osName)) {
	case "js":
		return OSWasm, ArchJS, true
	case "wasip1":
		return OSWasm, ArchWasip1, true
	}
	return "", "", false
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

// share authorizes minting temporary, artifact-bound download links
var ValidScopes = set.Of("read", "write", "share")

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
