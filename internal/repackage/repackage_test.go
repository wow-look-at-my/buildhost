package repackage

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
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

func TestDebPackageName(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"myapp", "myapp"},
		{"go-toolchain", "go-toolchain"},
		{"pr-reviewer-agent/server", "pr-reviewer-agent-server"},
		{"team/group/proj", "team-group-proj"},
		{"my_app", "my-app"},
		{"a/b_c", "a-b-c"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, DebPackageName(tt.project), "DebPackageName(%q)", tt.project)
	}
}

// TestDebRepackage_NamespacedName proves a slash-namespaced project produces a
// valid Debian package: the control Package field, the .deb filename, and the
// installed binary path all use the folded name (slash -> dash). dpkg rejects a
// Package name containing '/', so this is what makes apt usable for namespaced
// projects.
func TestDebRepackage_NamespacedName(t *testing.T) {
	rp := &Deb{}
	input := makeInput()
	input.Project.Name = "pr-reviewer-agent/server"
	ctx := context.Background()

	output, err := rp.Repackage(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, "pr-reviewer-agent-server_1.2.3_amd64.deb", output.Filename)

	data, err := io.ReadAll(output.Reader)
	require.NoError(t, err)
	require.NoError(t, output.Reader.Close())

	members := readArMembers(t, data)
	require.Contains(t, members, "control.tar.gz")
	require.Contains(t, members, "data.tar.gz")

	control := tarGzEntries(t, members["control.tar.gz"])
	require.Contains(t, control, "./control")
	assert.Contains(t, control["./control"], "Package: pr-reviewer-agent-server\n")
	assert.NotContains(t, control["./control"], "Package: pr-reviewer-agent/server")

	dataEntries := tarGzEntries(t, members["data.tar.gz"])
	_, ok := dataEntries["./usr/bin/pr-reviewer-agent-server"]
	assert.True(t, ok, "expected binary at ./usr/bin/pr-reviewer-agent-server, got entries: %v", dataEntries)
}

// readArMembers parses a Unix `ar` archive (the .deb container) into a map of
// member name -> body. Each member has a fixed 60-byte header; the body is
// followed by a single '\n' pad byte when its length is odd.
func readArMembers(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	const magic = "!<arch>\n"
	require.True(t, bytes.HasPrefix(data, []byte(magic)), "missing ar magic")
	out := map[string][]byte{}
	for p := len(magic); p+60 <= len(data); {
		hdr := data[p : p+60]
		p += 60
		name := strings.TrimRight(string(hdr[0:16]), " ")
		name = strings.TrimSuffix(name, "/") // GNU ar may suffix names with '/'
		size, err := strconv.Atoi(strings.TrimSpace(string(hdr[48:58])))
		require.NoError(t, err)
		require.LessOrEqual(t, p+size, len(data), "ar member %q overruns archive", name)
		out[name] = data[p : p+size]
		p += size
		if size%2 == 1 {
			p++
		}
	}
	return out
}

// tarGzEntries gunzips and untars data into a map of entry name -> contents.
func tarGzEntries(t *testing.T, gzData []byte) map[string]string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(gzData))
	require.NoError(t, err)
	defer gr.Close()
	tr := tar.NewReader(gr)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		b, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[hdr.Name] = string(b)
	}
	return out
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
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &db.Project{Name: "testapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "v1.2.3", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	key, size, err := store.Put(ctx, strings.NewReader(string(testBinary)))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	rp := &OCI{Store: store, DB: d}
	input := makeInput()
	input.Artifact = *a

	output, err := rp.Repackage(ctx, input)
	require.Nil(t, err)
	require.NotEqual(t, int64(0), output.Size)
	assert.True(t, strings.HasSuffix(output.Filename, "-oci-manifest.json"))

	data, err := io.ReadAll(output.Reader)
	require.Nil(t, err)

	body := string(data)
	assert.Contains(t, body, "schemaVersion")
	assert.Contains(t, body, "application/vnd.oci.image.manifest.v1+json")
	assert.Contains(t, body, "sha256:")

	require.NotNil(t, output.Metadata)
	assert.Equal(t, "linux", output.Metadata["os"])
	assert.Equal(t, "amd64", output.Metadata["arch"])

	// Verify config and layer blobs were stored
	_, _, _, _, err = d.GetPackagedArtifact(ctx, a.ID, "oci-config")
	require.NoError(t, err)
	_, _, _, _, err = d.GetPackagedArtifact(ctx, a.ID, "oci-layer")
	require.NoError(t, err)
}

// --- Format tests ---

func TestFormats(t *testing.T) {
	tests := []struct {
		rp     Repackager
		format Format
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

	proj := &db.Project{Name: "empty-proj", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
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

	proj := &db.Project{Name: "myapp", Description: "test app", Homepage: "https://example.com", License: "MIT", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	// Store a fake binary.
	binaryContent := "fake-binary-content"
	key, size, err := store.Put(ctx, strings.NewReader(binaryContent))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID:  rel.ID,
		OS:         db.OSLinux,
		Arch:       db.ArchAMD64,
		Kind:       db.KindAssets, // Use Assets to skip strip attempt.
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

	proj := &db.Project{Name: "binapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	// Store a fake binary that is NOT a real ELF (strip will fail, but that is handled gracefully).
	binaryContent := "not-a-real-elf-binary"
	key, size, err := store.Put(ctx, strings.NewReader(binaryContent))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID:  rel.ID,
		OS:         db.OSLinux,
		Arch:       db.ArchAMD64,
		Kind:       db.KindBinary,
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

func TestGenerator_Generate(t *testing.T) {
	d := openTestDB(t)
	store := openTestStore(t)
	ctx := context.Background()

	proj := &db.Project{Name: "genapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))

	key, size, err := store.Put(ctx, strings.NewReader(string(testBinary)))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindAssets, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	gen := NewGenerator(store, d, t.TempDir())
	require.True(t, gen.Supports(FormatTarGZ))
	require.False(t, gen.Supports(Format("bogus")))

	out, err := gen.Generate(ctx, FormatTarGZ, *proj, *rel, *a, "https://example.com")
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.True(t, strings.HasSuffix(out.Filename, ".tar.gz"))
	// tar.gz streams, so its length isn't known up front (SizeUnknown); verify it
	// produced a non-empty archive by reading it.
	assert.Equal(t, SizeUnknown, out.Size)
	data, err := io.ReadAll(out.Reader)
	out.Reader.Close()
	require.NoError(t, err)
	assert.Greater(t, len(data), 0)
}

func TestGenerator_Generate_UnsupportedFormat(t *testing.T) {
	d := openTestDB(t)
	store := openTestStore(t)

	gen := NewGenerator(store, d, t.TempDir())
	_, err := gen.Generate(context.Background(), Format("bogus"), db.Project{}, db.Release{}, db.Artifact{}, "https://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
}
