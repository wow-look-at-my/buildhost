package repackage

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

var testBinary = []byte("#!/bin/sh\necho hello\n")

func makeInput() Input {
	return Input{
		Project: model.Project{
			Name:		"testapp",
			Description:	"A test application",
			Homepage:	"https://example.com",
			License:	"MIT",
		},
		Release: model.Release{
			Version:	"v1.2.3",
			VersionNum:	1,
		},
		Artifact: model.Artifact{
			OS:	model.OSLinux,
			Arch:	model.ArchAMD64,
			Kind:	model.KindBinary,
		},
		Data:		testBinary,
		BaseURL:	"https://builds.example.com",
	}
}

// --- Applicability tests ---

func TestTarGZApplicable(t *testing.T) {
	rp := &TarGZ{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestTarXZApplicable(t *testing.T) {
	rp := &TarXZ{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestTarZSTApplicable(t *testing.T) {
	rp := &TarZST{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestZipApplicable(t *testing.T) {
	rp := &Zip{}
	for _, kind := range []model.Kind{model.KindBinary, model.KindLibrary, model.KindAssets, model.KindArchive} {
		for _, os := range []model.OS{model.OSLinux, model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
			a := model.Artifact{OS: os, Kind: kind}
			assert.True(t, rp.Applicable(a))

		}
	}
}

func TestDebApplicable(t *testing.T) {
	rp := &Deb{}

	linuxArtifact := model.Artifact{OS: model.OSLinux, Kind: model.KindBinary}
	assert.True(t, rp.Applicable(linuxArtifact))

	for _, os := range []model.OS{model.OSDarwin, model.OSWindows, model.OSFreeBSD} {
		a := model.Artifact{OS: os, Kind: model.KindBinary}
		assert.False(t, rp.Applicable(a))

	}
}

func TestBrewApplicable(t *testing.T) {
	rp := &Brew{}

	// Linux and Darwin binaries are applicable
	for _, os := range []model.OS{model.OSLinux, model.OSDarwin} {
		a := model.Artifact{OS: os, Kind: model.KindBinary}
		assert.True(t, rp.Applicable(a))

	}

	// Assets kind is not applicable even on linux/darwin
	for _, os := range []model.OS{model.OSLinux, model.OSDarwin} {
		a := model.Artifact{OS: os, Kind: model.KindAssets}
		assert.False(t, rp.Applicable(a))

	}

	// Windows and FreeBSD are not applicable
	for _, os := range []model.OS{model.OSWindows, model.OSFreeBSD} {
		a := model.Artifact{OS: os, Kind: model.KindBinary}
		assert.False(t, rp.Applicable(a))

	}
}

func TestNPMApplicable(t *testing.T) {
	rp := &NPM{}

	// Binary, assets, archive are applicable
	for _, kind := range []model.Kind{model.KindBinary, model.KindAssets, model.KindArchive} {
		a := model.Artifact{OS: model.OSLinux, Kind: kind}
		assert.True(t, rp.Applicable(a))

	}

	// Library is not applicable
	a := model.Artifact{OS: model.OSLinux, Kind: model.KindLibrary}
	assert.False(t, rp.Applicable(a))

}

func TestOCIApplicable(t *testing.T) {
	rp := &OCI{}

	// Binary and archive are applicable
	for _, kind := range []model.Kind{model.KindBinary, model.KindArchive} {
		a := model.Artifact{OS: model.OSLinux, Kind: kind}
		assert.True(t, rp.Applicable(a))

	}

	// Library and assets are not applicable
	for _, kind := range []model.Kind{model.KindLibrary, model.KindAssets} {
		a := model.Artifact{OS: model.OSLinux, Kind: kind}
		assert.False(t, rp.Applicable(a))

	}
}

// --- Repackage tests ---

func TestTarGZRepackage(t *testing.T) {
	rp := &TarGZ{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".tar.gz"))

	// Verify it contains the expected file
	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	gr, err := gzip.NewReader(bytes.NewReader(data))
	require.Nil(t, err)

	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	require.Nil(t, err)

	assert.Equal(t, "testapp", hdr.Name)

	contents, _ := io.ReadAll(tr)
	assert.True(t, bytes.Equal(contents, testBinary))

}

func TestTarXZRepackage(t *testing.T) {
	rp := &TarXZ{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".tar.xz"))

	// Verify by decompressing with xz and reading tar
	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	// The xz package is used in production; here just verify non-empty output
	// with the correct xz magic bytes (0xFD, '7', 'z', 'X', 'Z', 0x00)
	require.GreaterOrEqual(t, len(data), 6)

	xzMagic := []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}
	assert.True(t, bytes.Equal(data[:6], xzMagic))

}

func TestTarZSTRepackage(t *testing.T) {
	rp := &TarZST{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".tar.zst"))

	// Verify zstd magic number: 0x28 0xB5 0x2F 0xFD
	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	require.GreaterOrEqual(t, len(data), 4)

	zstMagic := []byte{0x28, 0xB5, 0x2F, 0xFD}
	assert.True(t, bytes.Equal(data[:4], zstMagic))

}

func TestZipRepackage(t *testing.T) {
	rp := &Zip{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".zip"))

	// Verify by opening with archive/zip
	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.Nil(t, err)

	require.Equal(t, 1, len(zr.File))

	assert.Equal(t, "testapp", zr.File[0].Name)

	rc, err := zr.File[0].Open()
	require.Nil(t, err)

	contents, _ := io.ReadAll(rc)
	rc.Close()
	assert.True(t, bytes.Equal(contents, testBinary))

}

func TestDebRepackage(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".deb"))

	// Verify ar archive magic
	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	arMagic := "!<arch>\n"
	require.GreaterOrEqual(t, len(data), len(arMagic))

	assert.Equal(t, arMagic, string(data[:len(arMagic)]))

}

func TestBrewRepackage(t *testing.T) {
	rp := &Brew{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".rb"))

	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	body := string(data)
	assert.Contains(t, body, "class")

	assert.Contains(t, body, "Formula")

}

func TestNPMRepackage(t *testing.T) {
	rp := &NPM{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, ".tgz"))

	// Verify it is a valid gzipped tar containing package/package.json
	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	gr, err := gzip.NewReader(bytes.NewReader(data))
	require.Nil(t, err)

	tr := tar.NewReader(gr)

	foundPackageJSON := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.Nil(t, err)

		if hdr.Name == "package/package.json" {
			foundPackageJSON = true
		}
	}
	assert.True(t, foundPackageJSON)

}

func TestOCIRepackage(t *testing.T) {
	rp := &OCI{}
	input := makeInput()
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)

	require.NotEqual(t, int64(0), output.Size)

	assert.True(t, strings.HasSuffix(output.Filename, "-oci.json"))

	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	body := string(data)
	assert.Contains(t, body, "schemaVersion")

	assert.Contains(t, body, "application/vnd.oci.image.manifest.v1+json")

	// Verify metadata
	require.NotNil(t, output.Metadata)

	assert.Equal(t, "linux", output.Metadata["os"])

	assert.Equal(t, "amd64", output.Metadata["arch"])

}

// --- Format tests ---

func TestFormats(t *testing.T) {
	tests := []struct {
		rp	Repackager
		format	Format
	}{
		{&TarGZ{}, FormatTarGZ},
		{&TarXZ{}, FormatTarXZ},
		{&TarZST{}, FormatTarZST},
		{&Zip{}, FormatZip},
		{&Deb{}, FormatDeb},
		{&Brew{}, FormatBrew},
		{&NPM{}, FormatNPM},
		{&OCI{}, FormatOCI},
	}

	for _, tt := range tests {
		got := tt.rp.Format()
		assert.Equal(t, tt.format, got)

	}
}

// --- Orchestrator tests ---

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	return d
}

func openTestStore(t *testing.T) *storage.Filesystem {
	t.Helper()
	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)
	return store
}

func TestOrchestrator_PublishRelease_NoArtifacts(t *testing.T) {
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &model.Project{Name: "empty-proj", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	o := NewOrchestrator(store, d)

	err := o.PublishRelease(ctx, *proj, *rel)
	require.NoError(t, err)

	// Verify the release was published.
	got, err := d.GetRelease(ctx, proj.ID, "1.0.0")
	require.NoError(t, err)
	assert.True(t, got.Published)
}

func TestOrchestrator_PublishRelease_WithArtifact(t *testing.T) {
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &model.Project{Name: "myapp", Description: "test app", Homepage: "https://example.com", License: "MIT", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	// Store a fake binary.
	binaryContent := "fake-binary-content"
	key, size, err := store.Put(ctx, strings.NewReader(binaryContent))
	require.NoError(t, err)

	a := &model.Artifact{
		ReleaseID:  rel.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindAssets, // Use Assets to skip strip attempt.
		StorageKey: key,
		Size:       size,
		SHA256:     key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	o := NewOrchestrator(store, d)

	err = o.PublishRelease(ctx, *proj, *rel)
	require.NoError(t, err)

	got, err := d.GetRelease(ctx, proj.ID, "1.0.0")
	require.NoError(t, err)
	assert.True(t, got.Published)
}

func TestOrchestrator_PublishRelease_BinaryKind_AttemptsStrip(t *testing.T) {
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &model.Project{Name: "binapp", Versioning: model.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	// Store a fake binary that is NOT a real ELF (strip will fail, but that is handled gracefully).
	binaryContent := "not-a-real-elf-binary"
	key, size, err := store.Put(ctx, strings.NewReader(binaryContent))
	require.NoError(t, err)

	a := &model.Artifact{
		ReleaseID:  rel.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindBinary,
		StorageKey: key,
		Size:       size,
		SHA256:     key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	o := NewOrchestrator(store, d)

	// Should not error even when strip fails (it logs a warning and continues).
	err = o.PublishRelease(ctx, *proj, *rel)
	require.NoError(t, err)

	// Release should be published regardless.
	got, err := d.GetRelease(ctx, proj.ID, "1.0.0")
	require.NoError(t, err)
	assert.True(t, got.Published)
}

func TestNewOrchestrator(t *testing.T) {
	d := openTestDB(t)
	store := openTestStore(t)

	o := NewOrchestrator(store, d)
	require.NotNil(t, o)
	assert.Equal(t, d, o.DB)
	assert.Equal(t, store, o.Store)
}
