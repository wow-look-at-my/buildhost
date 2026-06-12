package brew

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store, Gen: repackage.NewGenerator(store, d, t.TempDir())}
	return h, d, store
}

func withProject(ctx context.Context, p *db.Project) context.Context {
	return auth.WithProject(ctx, p)
}

func TestServeFormula_NotRB(t *testing.T) {
	h, _, _ := setupTest(t)

	proj := &db.Project{Name: "myapp"}
	req := httptest.NewRequest("GET", "/myapp.txt", nil)
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeFormula_NoRelease(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/myapp.rb", nil)
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeFormula_NoBrewPackage(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	// Create artifact -- on-demand generation means brew formula is
	// generated from the binary, no packaged_artifacts row needed.
	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/myapp.rb", nil)
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-ruby", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServeFormula_Success(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	// On-demand generation: no CreatePackagedArtifact needed.
	req := httptest.NewRequest("GET", "/myapp.rb", nil)
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-ruby", rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestServeFormula_EmitsAllSupportedPlatforms(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "v1.2.3", VersionNum: 1002003}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	addArtifact := func(osName db.OS, arch db.Arch, body string) db.Artifact {
		t.Helper()
		key, size, err := store.Put(ctx, strings.NewReader(body))
		require.NoError(t, err)
		a := &db.Artifact{
			ReleaseID: rel.ID, OS: osName, Arch: arch,
			Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
		}
		require.NoError(t, d.CreateArtifact(ctx, a))
		return *a
	}

	addArtifact(db.OSLinux, db.ArchAMD64, "linux-amd64")
	darwinARM := addArtifact(db.OSDarwin, db.ArchARM64, "darwin-arm64")
	addArtifact(db.OSLinux, db.ArchARM64, "linux-arm64")
	addArtifact(db.OSDarwin, db.ArchAMD64, "darwin-amd64")
	addArtifact(db.OSWindows, db.ArchAMD64, "windows-amd64")

	req := httptest.NewRequest("GET", "/go-toolchain.rb", nil)
	req.Host = "brew.example.com:18080"
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `version "1.2.3"`)
	assert.Contains(t, body, "on_macos do")
	assert.Contains(t, body, "on_linux do")
	assert.Contains(t, body, "on_arm do")
	assert.Contains(t, body, "on_intel do")
	assert.Contains(t, body, "os=darwin")
	assert.Contains(t, body, "arch=arm64")
	assert.Contains(t, body, "fmt=tar.gz")
	assert.Contains(t, body, "v=v1.2.3")
	assert.NotContains(t, body, "os=windows")

	tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, *proj, *rel, darwinARM, "https://example.com")
	require.NoError(t, err)
	data, err := io.ReadAll(tgz.Reader)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	assert.Contains(t, body, fmt.Sprintf(`sha256 "%x"`, sum))
}

func TestServeTap_GeneratesDumbGitRepo(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSDarwin, Arch: db.ArchARM64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	private := &db.Project{Name: "secret-tool", IsPrivate: true, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, private))

	req := httptest.NewRequest("GET", "/brew/tap.git/info/refs?service=git-upload-pack", nil)
	req.Host = "git.example.com"
	req.SetPathValue("path", "info/refs")
	rec := httptest.NewRecorder()
	h.ServeTap(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	infoRefs := rec.Body.String()
	assert.Contains(t, infoRefs, "refs/heads/main")

	repo, err := h.buildTapRepo(req)
	require.NoError(t, err)
	assert.Equal(t, []byte("ref: refs/heads/main\n"), repo["HEAD"])
	assert.Contains(t, string(repo["info/refs"]), "refs/heads/main")
	assert.NotContains(t, fmt.Sprint(repo), "secret-tool")
}

func TestRedirectTap_ToGitService(t *testing.T) {
	h, _, _ := setupTest(t)
	req := httptest.NewRequest("GET", "/tap.git/info/refs?service=git-upload-pack", nil)
	req.Host = "brew.example.com:18080"
	req.SetPathValue("path", "info/refs")
	rec := httptest.NewRecorder()

	h.RedirectTap(rec, req)

	require.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "https://git.example.com:18080/brew/tap.git/info/refs?service=git-upload-pack", rec.Header().Get("Location"))
}

func TestParseRoute(t *testing.T) {
	req := httptest.NewRequest("GET", "/myapp", nil)
	req.SetPathValue("project", "myapp")
	ri := parseRoute(req)
	assert.Equal(t, "myapp", ri.ProjectName())
	assert.Equal(t, auth.ReadAccess, ri.Access())
}
