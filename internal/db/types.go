package db

import (
	"fmt"
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

// Platform is one (os, arch) slot. An artifact occupies at least one; an APE
// occupies several with a single file.
type Platform struct {
	OS   OS   `json:"os"`
	Arch Arch `json:"arch"`
}

func (p Platform) String() string { return string(p.OS) + "/" + string(p.Arch) }

// ParsePlatform reads one "os/arch" pair, accepting the same alias spellings
// NormalizeOS/NormalizeArch accept so a publisher can pass platform names
// through verbatim. The pair must be coherent (CompatiblePlatform).
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
	seen := make(map[Platform]bool, len(elems))
	for _, elem := range elems {
		if strings.TrimSpace(elem) == "" {
			return nil, fmt.Errorf("empty platform in %q", spec)
		}
		p, err := ParsePlatform(elem)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, fmt.Errorf("duplicate platform %q", p)
		}
		seen[p] = true
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
// always holds at least the canonical slot, so a consumer reads one field
// whether the artifact is an ordinary per-platform build or a single file
// covering several.
type ArtifactWithPlatforms struct {
	Artifact
	Platforms []Platform `json:"platforms"`
}

// MultiPlatform reports whether this one file covers more than one platform --
// what makes it a single download link instead of several.
func (a ArtifactWithPlatforms) MultiPlatform() bool { return len(a.Platforms) > 1 }

// PlatformArtifact is an artifact viewed as ONE of the platforms it covers:
// Artifact.OS/Arch are the covered platform, not necessarily the row's
// canonical slot.
type PlatformArtifact struct {
	Artifact
	// CacheSuffix distinguishes this platform's derived packages from the same
	// artifact's other platforms in packaged_artifacts, which is keyed on
	// (artifact_id, format). Without it a deb built for linux/arm64 would be
	// served as the linux/amd64 deb of the same file. It is "" for the
	// canonical slot, so every pre-existing cache row keeps its exact key.
	CacheSuffix string
}

// CacheFormat is the packaged_artifacts format key for a derived package of
// this artifact at this platform.
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

// NormalizeLegacyWasmPair maps the deprecated GOOS/GOARCH-ordered
// WebAssembly pair -- (os=js, arch=wasm) or (os=wasip1, arch=wasm), the
// `name_GOOS_GOARCH` filename convention currently-released go-toolchain
// autoreleases derive upload parameters from -- to the canonical
// (OSWasm, flavor arch) form. It is a parse-time alias only: "js" is never
// stored or surfaced as an os in artifact rows, URLs, or canonical query
// params. Returns ("", "", false) when the pair is not the legacy form
// (callers then proceed with normal per-value normalization). Deprecated
// from day one: publishers should upload os=wasm, arch=js|wasip1.
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
