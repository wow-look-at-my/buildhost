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
	KindBinary  Kind = "binary"
	KindLibrary Kind = "library"
	KindAssets  Kind = "assets"
	KindArchive Kind = "archive"
)

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

func ValidKind(s string) bool {
	switch Kind(s) {
	case KindBinary, KindLibrary, KindAssets, KindArchive:
		return true
	}
	return false
}

type APIToken = ApiToken
type OIDCPolicy = OidcPolicy

var ValidScopes = map[string]bool{
	"read":  true,
	"write": true,
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
