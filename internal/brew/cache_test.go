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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// seedBrewProject creates a public project with one published release and one
// darwin/arm64 binary artifact, and returns all three.
func seedBrewProject(t *testing.T, d *db.DB, store *storage.Filesystem, name, body string) (*db.Project, *db.Release, *db.Artifact) {
	t.Helper()
	ctx := context.Background()

	proj := &db.Project{Name: name, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader(body))
	require.NoError(t, err)
	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSDarwin, Arch: db.ArchARM64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))
	return proj, rel, a
}

func getTap(t *testing.T, h *Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/brew/tap.git/"+path, nil)
	req.Host = host
	req.SetPathValue("path", path)
	rec := httptest.NewRecorder()
	h.ServeTap(rec, req)
	return rec
}

// TestTarGZGenerationDeterministic pins the precondition the tar.gz digest
// cache (and Homebrew's own checksum verification of on-demand downloads)
// relies on: two independent generations of the same artifact yield identical
// bytes, regardless of wall clock and of the request base URL. The >1s sleep
// would expose any second-granularity timestamp leaking into the tar or gzip
// headers.
func TestTarGZGenerationDeterministic(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, rel, a := seedBrewProject(t, d, store, "myapp", "deterministic-binary-bytes")

	gen := func(baseURL string) []byte {
		t.Helper()
		out, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, *proj, *rel, *a, baseURL)
		require.NoError(t, err)
		data, err := io.ReadAll(out.Reader)
		require.NoError(t, err)
		require.NoError(t, out.Reader.Close())
		return data
	}

	first := gen("https://alpha.example")
	time.Sleep(1100 * time.Millisecond)
	second := gen("https://beta.example")
	require.Equal(t, first, second)
}

func TestServeFormula_CachesTarGZDigest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, rel, a := seedBrewProject(t, d, store, "myapp", "binary-bytes")

	fetch := func() string {
		t.Helper()
		req := httptest.NewRequest("GET", "/myapp.rb", nil)
		req = req.WithContext(withProject(req.Context(), proj))
		rec := httptest.NewRecorder()
		h.ServeFormula(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}

	// No cache row before the first fetch.
	_, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "tar.gz")
	require.ErrorIs(t, err, db.ErrNotFound)

	body := fetch()

	// The first fetch computed the digest and stored it, and the stored digest
	// matches an independent generation of the same artifact -- i.e. the exact
	// tar.gz payload dl/static serve. The row records the SOURCE blob key (no
	// tar.gz is stored) plus the generated payload size.
	tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, *proj, *rel, *a, "https://elsewhere.example")
	require.NoError(t, err)
	payload, err := io.ReadAll(tgz.Reader)
	require.NoError(t, err)
	require.NoError(t, tgz.Reader.Close())
	want := fmt.Sprintf("%x", sha256.Sum256(payload))

	cachedKey, cachedSize, cachedSHA, _, err := d.GetPackagedArtifact(ctx, a.ID, "tar.gz")
	require.NoError(t, err)
	assert.Equal(t, want, cachedSHA)
	assert.Equal(t, int64(len(payload)), cachedSize)
	assert.Equal(t, a.StorageKey, cachedKey)
	assert.Contains(t, body, fmt.Sprintf("sha256 %q", want))

	// A second fetch reads the cached digest instead of regenerating: poison
	// the row with a sentinel and the served formula must carry the sentinel.
	sentinel := strings.Repeat("42", 32)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "tar.gz", a.StorageKey, cachedSize, sentinel, "x.tar.gz", "{}"))
	assert.Contains(t, fetch(), fmt.Sprintf("sha256 %q", sentinel))
}

func TestServeTap_SnapshotCachedAndExpires(t *testing.T) {
	oldTTL := tapCacheTTL
	tapCacheTTL = time.Hour
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	rec1 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec1.Code)
	body1 := rec1.Body.String()
	assert.Contains(t, body1, "refs/heads/main")

	// The snapshot is materialized as real files under the data-dir scratch
	// root ({TmpDir}/brew-tap/<build>/), in the dumb-HTTP git layout.
	snapRoot := filepath.Join(h.TmpDir, tapSnapshotDirName)
	entries, err := os.ReadDir(snapRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	snapDir := filepath.Join(snapRoot, entries[0].Name())
	for _, f := range []string{"HEAD", "info/refs", "refs/heads/main", "objects/info/packs"} {
		_, err := os.Stat(filepath.Join(snapDir, filepath.FromSlash(f)))
		assert.NoError(t, err, f)
	}

	// A loose object is served from the snapshot via the mmap path with the
	// git loose-object content type.
	commitSHA := strings.Fields(body1)[0]
	recObj := getTap(t, h, "git.example.com", "objects/"+commitSHA[:2]+"/"+commitSHA[2:])
	require.Equal(t, http.StatusOK, recObj.Code)
	assert.Equal(t, "application/x-git-loose-object", recObj.Header().Get("Content-Type"))
	assert.NotZero(t, recObj.Body.Len())

	// The zero-length objects/info/packs maps nothing and serves empty.
	recPacks := getTap(t, h, "git.example.com", "objects/info/packs")
	require.Equal(t, http.StatusOK, recPacks.Code)
	assert.Zero(t, recPacks.Body.Len())

	// Publish a second project. Within the TTL the tap still serves the one
	// existing build -- identical bytes, same snapshot dir -- so a brew
	// update's burst of requests sees a single consistent snapshot.
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")
	rec2 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, body1, rec2.Body.String())
	entries, err = os.ReadDir(snapRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, snapDir, filepath.Join(snapRoot, entries[0].Name()))

	// Force expiry: the next request rebuilds, swaps in a fresh snapshot dir,
	// removes the old one, and reflects the new publish.
	tapCacheTTL = 0
	rec3 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec3.Code)
	assert.NotEqual(t, body1, rec3.Body.String())

	req := httptest.NewRequest("GET", "/brew/tap.git/info/refs", nil)
	req.Host = "git.example.com"
	repo, err := h.buildTapRepo(req)
	require.NoError(t, err)
	assert.Equal(t, string(repo["info/refs"]), rec3.Body.String())

	entries, err = os.ReadDir(snapRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.NotEqual(t, snapDir, filepath.Join(snapRoot, entries[0].Name()))
}

func TestServeTap_SnapshotKeyedByHost(t *testing.T) {
	oldTTL := tapCacheTTL
	tapCacheTTL = time.Hour
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "myapp", "binary-bytes")

	recAlpha := getTap(t, h, "git.alpha.test", "info/refs")
	require.Equal(t, http.StatusOK, recAlpha.Code)
	recBeta := getTap(t, h, "git.beta.test", "info/refs")
	require.Equal(t, http.StatusOK, recBeta.Code)

	// Different hosts bake different download URLs into the formulas, so the
	// builds differ: beta must never be handed alpha's cached snapshot.
	assert.NotEqual(t, recAlpha.Body.String(), recBeta.Body.String())

	reqBeta := httptest.NewRequest("GET", "/brew/tap.git/info/refs", nil)
	reqBeta.Host = "git.beta.test"
	repoBeta, err := h.buildTapRepo(reqBeta)
	require.NoError(t, err)
	assert.Equal(t, string(repoBeta["info/refs"]), recBeta.Body.String())

	// The cached beta snapshot serves beta's own loose objects byte-for-byte.
	served := false
	for path, want := range repoBeta {
		if !strings.HasPrefix(path, "objects/") || len(want) == 0 {
			continue
		}
		rec := getTap(t, h, "git.beta.test", path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Equal(t, want, rec.Body.Bytes(), path)
		served = true
		break
	}
	require.True(t, served, "no loose object found to fetch")

	// Flipping back to alpha within the TTL serves alpha's own cached snapshot
	// (per-key entries; beta's build never evicted it) byte-for-byte.
	recAlpha2 := getTap(t, h, "git.alpha.test", "info/refs")
	require.Equal(t, http.StatusOK, recAlpha2.Code)
	assert.Equal(t, recAlpha.Body.String(), recAlpha2.Body.String())
}

func TestServeTap_RejectsEscapingPaths(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "myapp", "binary-bytes")

	// Prime the snapshot, then plant a file two levels above it (directly
	// under TmpDir). The os.Root sandbox must refuse to serve it even though
	// the relative path resolves to an existing file.
	require.Equal(t, http.StatusOK, getTap(t, h, "git.example.com", "info/refs").Code)
	require.NoError(t, os.WriteFile(filepath.Join(h.TmpDir, "secret.txt"), []byte("s"), 0o644))

	rec := getTap(t, h, "git.example.com", "../../secret.txt")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
