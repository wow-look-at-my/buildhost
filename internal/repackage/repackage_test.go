package repackage

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

var testBinary = []byte("#!/bin/sh\necho hello\n")

func makeInput() Input {
	return Input{
		Project: db.Project{
			Name:        "testapp",
			Description: "A test application",
			Homepage:    "https://example.com",
			License:     "MIT",
		},
		Release: db.Release{
			Version:    "v1.2.3",
			VersionNum: 1,
		},
		Artifact: db.Artifact{
			OS:   db.OSLinux,
			Arch: db.ArchAMD64,
			Kind: db.KindBinary,
		},
		Reader:  bytes.NewReader(testBinary),
		Size:    int64(len(testBinary)),
		BaseURL: "https://builds.example.com",
	}
}

// --- Applicability tests ---

func TestTarGZApplicable(t *testing.T) {
	rp := &TarGZ{}
	for _, kind := range []db.Kind{db.KindBinary, db.KindLibrary, db.KindAssets, db.KindArchive} {
		for _, os := range []db.OS{db.OSLinux, db.OSDarwin, db.OSWindows, db.OSFreeBSD} {
			a := db.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestTarXZApplicable(t *testing.T) {
	rp := &TarXZ{}
	for _, kind := range []db.Kind{db.KindBinary, db.KindLibrary, db.KindAssets, db.KindArchive} {
		for _, os := range []db.OS{db.OSLinux, db.OSDarwin, db.OSWindows, db.OSFreeBSD} {
			a := db.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestTarZSTApplicable(t *testing.T) {
	rp := &TarZST{}
	for _, kind := range []db.Kind{db.KindBinary, db.KindLibrary, db.KindAssets, db.KindArchive} {
		for _, os := range []db.OS{db.OSLinux, db.OSDarwin, db.OSWindows, db.OSFreeBSD} {
			a := db.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestZipApplicable(t *testing.T) {
	rp := &Zip{}
	for _, kind := range []db.Kind{db.KindBinary, db.KindLibrary, db.KindAssets, db.KindArchive} {
		for _, os := range []db.OS{db.OSLinux, db.OSDarwin, db.OSWindows, db.OSFreeBSD} {
			a := db.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestDebApplicable(t *testing.T) {
	rp := &Deb{}

	linuxArtifact := db.Artifact{OS: db.OSLinux, Kind: db.KindBinary}
	assert.True(t, rp.Applicable(linuxArtifact))

	for _, os := range []db.OS{db.OSDarwin, db.OSWindows, db.OSFreeBSD} {
		a := db.Artifact{OS: os, Kind: db.KindBinary}
		assert.False(t, rp.Applicable(a))

	}
}

func TestBrewApplicable(t *testing.T) {
	rp := &Brew{}

	// Linux and Darwin binaries are applicable
	for _, os := range []db.OS{db.OSLinux, db.OSDarwin} {
		a := db.Artifact{OS: os, Kind: db.KindBinary}
		assert.True(t, rp.Applicable(a))

	}

	// Assets kind is not applicable even on linux/darwin
	for _, os := range []db.OS{db.OSLinux, db.OSDarwin} {
		a := db.Artifact{OS: os, Kind: db.KindAssets}
		assert.False(t, rp.Applicable(a))

	}

	// Windows and FreeBSD are not applicable
	for _, os := range []db.OS{db.OSWindows, db.OSFreeBSD} {
		a := db.Artifact{OS: os, Kind: db.KindBinary}
		assert.False(t, rp.Applicable(a))

	}
}

func TestNPMApplicable(t *testing.T) {
	rp := &NPM{}

	// Binary, assets, archive are applicable
	for _, kind := range []db.Kind{db.KindBinary, db.KindAssets, db.KindArchive} {
		a := db.Artifact{OS: db.OSLinux, Kind: kind}
		assert.True(t, rp.Applicable(a))

	}

	// Library is not applicable
	a := db.Artifact{OS: db.OSLinux, Kind: db.KindLibrary}
	assert.False(t, rp.Applicable(a))

}

func TestOCIApplicable(t *testing.T) {
	rp := &OCI{}

	// Only binary is applicable
	a := db.Artifact{OS: db.OSLinux, Kind: db.KindBinary}
	assert.True(t, rp.Applicable(a))

	// Archive, library, assets are not applicable
	for _, kind := range []db.Kind{db.KindArchive, db.KindLibrary, db.KindAssets} {
		a := db.Artifact{OS: db.OSLinux, Kind: kind}
		assert.False(t, rp.Applicable(a))
	}
}
