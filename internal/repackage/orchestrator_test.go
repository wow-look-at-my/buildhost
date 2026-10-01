package repackage

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
	d := openTestDB(t)
	store := openTestStore(t)

	o := NewOrchestrator(store, d)
	require.NotNil(t, o)
	assert.Equal(t, d, o.DB)
	assert.Equal(t, store, o.Store)
}

func TestGenerator_Generate(t *testing.T) {
	t.Serial()
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
	assert.Equal(t, SizeUnknown, out.Size)
	data, err := io.ReadAll(out.Reader)
	out.Reader.Close()
	require.NoError(t, err)
	assert.Greater(t, len(data), 0)
}

func TestGenerator_Generate_UnsupportedFormat(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	store := openTestStore(t)

	gen := NewGenerator(store, d, t.TempDir())
	_, err := gen.Generate(context.Background(), Format("bogus"), db.Project{}, db.Release{}, db.Artifact{}, "https://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
}
