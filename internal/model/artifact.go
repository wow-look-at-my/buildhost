package model

import "time"

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

type Artifact struct {
	ID                int64     `json:"id"`
	ReleaseID         int64     `json:"release_id"`
	OS                OS        `json:"os"`
	Arch              Arch      `json:"arch"`
	Kind              Kind      `json:"kind"`
	StorageKey        string    `json:"storage_key"`
	Size              int64     `json:"size"`
	SHA256            string    `json:"sha256"`
	StrippedStorageKey string   `json:"stripped_storage_key,omitempty"`
	StrippedSize      int64     `json:"stripped_size,omitempty"`
	StrippedSHA256    string    `json:"stripped_sha256,omitempty"`
	DebugStorageKey   string    `json:"debug_storage_key,omitempty"`
	DebugSize         int64     `json:"debug_size,omitempty"`
	Filename          string    `json:"filename,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}
