package brew

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

	dataDir := t.TempDir()
	tmp := filepath.Join(dataDir, "tmp")
	require.NoError(t, os.MkdirAll(tmp, 0o755))
	h := &Handler{DB: d, Store: store, Gen: repackage.NewGenerator(store, d, tmp), TmpDir: tmp, DataDir: dataDir}
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
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	// Create artifact -- on-demand generation means brew formula is
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
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
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

// The operator-set projects.create_service flag round-trips DB -> formula: the
func TestServeFormula_CreateServiceFlag(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSDarwin, Arch: db.ArchARM64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	serveBody := func() string {
		t.Helper()
		got, gerr := d.GetProject(ctx, "myapp")
		require.NoError(t, gerr)
		req := httptest.NewRequest("GET", "/myapp.rb", nil)
		req = req.WithContext(withProject(req.Context(), got))
		rec := httptest.NewRecorder()
		h.ServeFormula(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}

	assert.NotContains(t, serveBody(), "service do")

	require.NoError(t, d.SetProjectCreateService(ctx, proj.ID, true))
	body := serveBody()
	assert.Contains(t, body, "service do")
	assert.Contains(t, body, "run [opt_bin/\"myapp\"]")
	assert.Contains(t, body, "keep_alive successful_exit: false")
}

func TestServeFormula_EmitsAllSupportedPlatforms(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "v1.2.3", VersionNum: 1002003, GitBranch: db.LatestBranch}
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
	// Dual-OS: importable everywhere as-is, so no platform gate; the top-level
	assert.NotContains(t, body, "depends_on")
	assert.Contains(t, body, "\n  url \"https://dl.example.com:18080/go-toolchain?arch=amd64&fmt=tar.gz&os=linux&v=v1.2.3\"\n")

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
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
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

	recHead := getTap(t, h, "git.example.com", "HEAD")
	require.Equal(t, http.StatusOK, recHead.Code)
	assert.Equal(t, "ref: refs/heads/main\n", recHead.Body.String())

	files, err := h.buildTapFiles(req)
	require.NoError(t, err)
	assert.Contains(t, files, "Formula/go-toolchain.rb")
	assert.NotContains(t, tapFilesText(files), "secret-tool")
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
	h, _, _ := setupTest(t)
	req := httptest.NewRequest("GET", "/myapp", nil)
	req.SetPathValue("project", "myapp")
	ri := h.parseRoute(req)
	assert.Equal(t, "myapp", ri.ProjectName())
	assert.Equal(t, auth.ReadAccess, ri.Access())
}

// A tap formula's FILENAME folds the slash namespace ("gcc/pgo" ->
// gcc-pgo.rb), so the per-formula URL users copy out of the tap carries the
// folded name. The route must resolve it back to the real project -- while a
// literally named project always wins over a fold match.
func TestParseRoute_FoldedTapNameResolvesToProject(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	seedBrewProject(t, d, store, "gcc/pgo", "pgo-binary")

	req := httptest.NewRequest("GET", "/Formula/gcc-pgo.rb", nil)
	req.SetPathValue("project", "gcc-pgo")
	assert.Equal(t, "gcc/pgo", h.parseRoute(req).ProjectName())

	// No fold candidate: the literal name passes through untouched (404s in
	req = httptest.NewRequest("GET", "/Formula/no-such.rb", nil)
	req.SetPathValue("project", "no-such")
	assert.Equal(t, "no-such", h.parseRoute(req).ProjectName())

	// A PRIVATE project's folded name resolves only for a request that could
	secret := seedPrivateBrewProject(t, d, store, "ns/hidden", "hidden-binary")
	req = httptest.NewRequest("GET", "/Formula/ns-hidden.rb", nil)
	req.SetPathValue("project", "ns-hidden")
	assert.Equal(t, "ns-hidden", h.parseRoute(req).ProjectName())
	authed := withReadToken(httptest.NewRequest("GET", "/Formula/ns-hidden.rb", nil), &secret.ID)
	authed.SetPathValue("project", "ns-hidden")
	assert.Equal(t, "ns/hidden", h.parseRoute(authed).ProjectName())

	// A project literally named like the folded form wins over the fold.
	literal := &db.Project{Name: "gcc-pgo", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, literal))
	req = httptest.NewRequest("GET", "/Formula/gcc-pgo.rb", nil)
	req.SetPathValue("project", "gcc-pgo")
	assert.Equal(t, "gcc-pgo", h.parseRoute(req).ProjectName())
}

// BUG guard: a single-OS project's formula must carry a TOP-LEVEL url/sha256
// and a depends_on gate. With only on_<os> stanzas, Homebrew on the OTHER
// platform found no stable URL ("formula requires at least a URL") and the
// failed import poisoned the whole tap for that platform.
func TestServeFormula_LinuxOnlyCarriesTopLevelURLAndDependsOnLinux(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "gcc", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	key, size, err := store.Put(ctx, strings.NewReader("linux-only-binary"))
	require.NoError(t, err)
	a := db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, &a))

	req := httptest.NewRequest("GET", "/Formula/gcc.rb", nil)
	req.Host = "brew.example.com"
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "depends_on :linux")
	assert.NotContains(t, body, "depends_on :macos")

	// The top-level (stable) url/sha256 is the canonical linux/amd64 resource
	tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, *proj, *rel, a, "https://example.com")
	require.NoError(t, err)
	payload, err := io.ReadAll(tgz.Reader)
	require.NoError(t, err)
	require.NoError(t, tgz.Reader.Close())
	wantSHA := fmt.Sprintf("%x", sha256.Sum256(payload))
	wantURL := `url "https://dl.example.com/gcc?arch=amd64&fmt=tar.gz&os=linux&v=1.0.0"`
	assert.Contains(t, body, "\n  "+wantURL+"\n  sha256 \""+wantSHA+"\"\n")
	assert.Equal(t, 2, strings.Count(body, wantURL), "top-level url plus the on_linux block")
	assert.Equal(t, 2, strings.Count(body, fmt.Sprintf("sha256 %q", wantSHA)))
	assert.Contains(t, body, "on_linux do")
}

func TestServeFormula_MacOnlyDependsOnMacos(t *testing.T) {
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "mactool", "mac-binary") // darwin/arm64 only

	req := httptest.NewRequest("GET", "/Formula/mactool.rb", nil)
	req.Host = "brew.example.com"
	req = req.WithContext(withProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "depends_on :macos")
	assert.NotContains(t, body, "depends_on :linux")
	assert.Contains(t, body, "on_macos do")
}
