package apt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func setupSigningTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	tmpDir := t.TempDir()
	d, err := db.Open(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(filepath.Join(tmpDir, "blobs"), true)
	require.NoError(t, err)

	signer := NewSigner(tmpDir)
	require.True(t, signer.Available())

	h := &Handler{
		DB:     d,
		Store:  store,
		Gen:    repackage.NewGenerator(store, d, tmpDir),
		Signer: signer,
	}
	return h, d, store
}

func TestNewSigner_GeneratesKey(t *testing.T) {
	tmpDir := t.TempDir()
	s := NewSigner(tmpDir)
	assert.True(t, s.Available())
	assert.NotEmpty(t, s.Fingerprint())
}

func TestNewSigner_LoadsExistingKey(t *testing.T) {
	tmpDir := t.TempDir()
	s1 := NewSigner(tmpDir)
	fp1 := s1.Fingerprint()

	s2 := NewSigner(tmpDir)
	fp2 := s2.Fingerprint()

	assert.Equal(t, fp1, fp2)
}

func TestSigner_PublicKeyArmored(t *testing.T) {
	s := NewSigner(t.TempDir())
	key, err := s.PublicKeyArmored()
	require.NoError(t, err)
	assert.Contains(t, string(key), "-----BEGIN PGP PUBLIC KEY BLOCK-----")
	assert.Contains(t, string(key), "-----END PGP PUBLIC KEY BLOCK-----")
}

func TestSigner_ClearSign(t *testing.T) {
	s := NewSigner(t.TempDir())
	data := []byte("test content to sign")
	signed, err := s.ClearSign(data)
	require.NoError(t, err)
	assert.Contains(t, string(signed), "-----BEGIN PGP SIGNED MESSAGE-----")
	assert.Contains(t, string(signed), "test content to sign")
	assert.Contains(t, string(signed), "-----BEGIN PGP SIGNATURE-----")
}

func TestSigner_DetachedSign(t *testing.T) {
	s := NewSigner(t.TempDir())
	data := []byte("test content to sign")
	sig, err := s.DetachedSign(data)
	require.NoError(t, err)
	assert.Contains(t, string(sig), "-----BEGIN PGP SIGNATURE-----")
	assert.NotContains(t, string(sig), "test content to sign")
}

func TestServeInRelease_Signed(t *testing.T) {
	h, d, _ := setupSigningTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/InRelease", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/InRelease"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "-----BEGIN PGP SIGNED MESSAGE-----")
	assert.Contains(t, body, "Label: myapp")
	assert.Contains(t, body, "-----BEGIN PGP SIGNATURE-----")
}

func TestServeReleaseGPG(t *testing.T) {
	h, d, _ := setupSigningTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release.gpg", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/Release.gpg"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/pgp-signature", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "-----BEGIN PGP SIGNATURE-----")
}

func TestServeKeyASC(t *testing.T) {
	h, d, _ := setupSigningTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/key.asc", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "key.asc"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/pgp-keys", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "-----BEGIN PGP PUBLIC KEY BLOCK-----")
}

func TestServeRelease_WithHashes(t *testing.T) {
	h, d, store := setupSigningTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/Release"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "SHA256:")
	assert.Contains(t, body, "main/binary-amd64/Packages")
}

func TestServeReleaseGPG_NoSigner(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/dists/stable/Release.gpg", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "dists/stable/Release.gpg"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeKeyASC_NoSigner(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp/key.asc", nil)
	req = withRoute(req, proj, route{project: "myapp", subPath: "key.asc"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSigner_Fingerprint(t *testing.T) {
	s := NewSigner(t.TempDir())
	fp := s.Fingerprint()
	assert.Len(t, fp, 40)
}

func TestBuildRelease_NoHashes(t *testing.T) {
	content := buildRelease("myproject", nil)
	assert.Contains(t, content, "Origin: buildhost")
	assert.Contains(t, content, "Label: myproject")
	assert.NotContains(t, content, "SHA256:")
}

func TestBuildRelease(t *testing.T) {
	hashes := []hashEntry{
		{path: "main/binary-amd64/Packages", hash: "abc123", size: 100},
		{path: "main/binary-arm64/Packages", hash: "def456", size: 200},
	}
	content := buildRelease("myproject", hashes)
	assert.Contains(t, content, "Origin: buildhost")
	assert.Contains(t, content, "Label: myproject")
	assert.Contains(t, content, "SHA256:")
	assert.Contains(t, content, " abc123 100 main/binary-amd64/Packages")
	assert.Contains(t, content, " def456 200 main/binary-arm64/Packages")
}
