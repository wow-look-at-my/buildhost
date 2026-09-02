package brew

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/json"
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

	_, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "tar.gz")
	require.ErrorIs(t, err, db.ErrNotFound)

	body := fetch()

	tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, *proj, *rel, *a, "https://elsewhere.example")
	require.NoError(t, err)
	payload, err := io.ReadAll(tgz.Reader)
	require.NoError(t, err)
	require.NoError(t, tgz.Reader.Close())
	want := fmt.Sprintf("%x", sha256.Sum256(payload))

	cachedKey, cachedSize, cachedSHA, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "tar.gz")
	require.NoError(t, err)
	assert.Equal(t, want, cachedSHA)
	assert.Equal(t, int64(len(payload)), cachedSize)
	assert.Equal(t, a.StorageKey, cachedKey)
	assert.Contains(t, body, fmt.Sprintf("sha256 %q", want))

	sentinel := strings.Repeat("42", 32)
	// Marshalled, not formatted: %q is Go quoting, which is not JSON escaping.
	meta, err := json.Marshal(map[string]string{"transform": repackage.TransformVersion})
	require.NoError(t, err)
	currentMeta := string(meta)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "tar.gz", a.StorageKey, cachedSize, sentinel, "x.tar.gz", currentMeta))
	assert.Contains(t, fetch(), fmt.Sprintf("sha256 %q", sentinel))
}

// A cached digest describes the artifact AFTER download-time transformation
// (stripping). If that transformation changes, every stored digest describes
func TestServeFormula_StaleTransformDigestRecomputed(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, rel, a := seedBrewProject(t, d, store, "myapp", "binary-bytes")

	tgz, err := h.Gen.Generate(ctx, repackage.FormatTarGZ, *proj, *rel, *a, "https://elsewhere.example")
	require.NoError(t, err)
	payload, err := io.ReadAll(tgz.Reader)
	require.NoError(t, err)
	require.NoError(t, tgz.Reader.Close())
	want := fmt.Sprintf("%x", sha256.Sum256(payload))

	sentinel := strings.Repeat("42", 32)
	for name, metadata := range map[string]string{
		"row predating the transform field": "{}",
		"row from a different transform":    `{"transform":"strip-something-else"}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "tar.gz", a.StorageKey, int64(len(payload)), sentinel, "x.tar.gz", metadata))

			req := httptest.NewRequest("GET", "/myapp.rb", nil)
			req = req.WithContext(withProject(req.Context(), proj))
			rec := httptest.NewRecorder()
			h.ServeFormula(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)

			assert.NotContains(t, rec.Body.String(), sentinel, "a stale digest must not be served")
			assert.Contains(t, rec.Body.String(), fmt.Sprintf("sha256 %q", want))

			// The stale row is replaced in place, stamped with the current version.
			_, _, cachedSHA, _, metadata, err := d.GetPackagedArtifact(ctx, a.ID, "tar.gz")
			require.NoError(t, err)
			assert.Equal(t, want, cachedSHA)
			assert.Contains(t, metadata, repackage.TransformVersion)
		})
	}
}

func TestServeTap_LineageCachedAndAppendsOnChange(t *testing.T) {
	oldTTL := tapCacheTTL
	tapCacheTTL = time.Hour
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	rec1 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec1.Code)
	body1 := rec1.Body.String()
	assert.Contains(t, body1, "refs/heads/main")

	// The lineage is materialized as real files under the PERSISTENT data dir
	histRoot := h.tapHistoryRoot()
	assert.Equal(t, filepath.Join(h.DataDir, "brew-tap"), histRoot)
	entries, err := os.ReadDir(histRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	linDir := filepath.Join(histRoot, entries[0].Name())
	for _, f := range []string{"HEAD", "info/refs", "refs/heads/main", "objects/info/packs"} {
		_, err := os.Stat(filepath.Join(linDir, filepath.FromSlash(f)))
		assert.NoError(t, err, f)
	}

	// A loose object is served from the lineage via the mmap path with the
	commitSHA := strings.Fields(body1)[0]
	require.Equal(t, commitSHA, readTapTip(linDir))
	recObj := getTap(t, h, "git.example.com", "objects/"+commitSHA[:2]+"/"+commitSHA[2:])
	require.Equal(t, http.StatusOK, recObj.Code)
	assert.Equal(t, "application/x-git-loose-object", recObj.Header().Get("Content-Type"))
	assert.NotZero(t, recObj.Body.Len())

	recPacks := getTap(t, h, "git.example.com", "objects/info/packs")
	require.Equal(t, http.StatusOK, recPacks.Code)
	assert.Zero(t, recPacks.Body.Len())

	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")
	rec2 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, body1, rec2.Body.String())
	require.Equal(t, commitSHA, readTapTip(linDir))

	// Force expiry: the next request rebuilds IN the same lineage and appends
	tapCacheTTL = 0
	rec3 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec3.Code)
	require.NotEqual(t, body1, rec3.Body.String())

	newTip := strings.Fields(rec3.Body.String())[0]
	require.Equal(t, newTip, readTapTip(linDir))
	assert.NotEqual(t, commitSHA, newTip)
	assert.Equal(t, commitSHA, readCommitParent(t, linDir, newTip),
		"the new tip must be a child of the previous tip")

	// The previous tip's object is STILL served (append-only store): a client
	recOld := getTap(t, h, "git.example.com", "objects/"+commitSHA[:2]+"/"+commitSHA[2:])
	require.Equal(t, http.StatusOK, recOld.Code)

	entries, err = os.ReadDir(histRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1, "a rebuild reuses the lineage dir, never a fresh one")
}

// readCommitParent parses the "parent <sha>" header out of a stored loose
// commit object ("" when the commit is a root).
func readCommitParent(t *testing.T, dir, commitSHA string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "objects", commitSHA[:2], commitSHA[2:]))
	require.NoError(t, err)
	zr, err := zlib.NewReader(bytes.NewReader(b))
	require.NoError(t, err)
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	require.NoError(t, err)
	for _, line := range strings.Split(string(raw), "\n") {
		if p, ok := strings.CutPrefix(line, "parent "); ok {
			return p
		}
	}
	return ""
}

// Rebuilding with UNCHANGED content must reuse the tip commit -- the periodic
// TTL rebuilds may not grow the history or move the ref.
func TestServeTap_UnchangedContentKeepsTipSHA(t *testing.T) {
	oldTTL := tapCacheTTL
	tapCacheTTL = 0 // every request re-checks
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	rec1 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec1.Code)
	rec2 := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, rec1.Body.String(), rec2.Body.String())

	histRoot := h.tapHistoryRoot()
	entries, err := os.ReadDir(histRoot)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	linDir := filepath.Join(histRoot, entries[0].Name())
	tip := readTapTip(linDir)
	require.Equal(t, strings.Fields(rec1.Body.String())[0], tip)
	assert.Empty(t, readCommitParent(t, linDir, tip), "no spurious chained commits from no-op rebuilds")
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
	assert.NotEqual(t, recAlpha.Body.String(), recBeta.Body.String())

	// Each host gets its own persisted lineage; the served tip matches beta's
	entries, err := os.ReadDir(h.tapHistoryRoot())
	require.NoError(t, err)
	require.Len(t, entries, 2)

	betaTip := strings.Fields(recBeta.Body.String())[0]
	betaDir := ""
	for _, e := range entries {
		if readTapTip(filepath.Join(h.tapHistoryRoot(), e.Name())) == betaTip {
			betaDir = filepath.Join(h.tapHistoryRoot(), e.Name())
		}
	}
	require.NotEmpty(t, betaDir, "no lineage carries beta's tip")

	want, err := os.ReadFile(filepath.Join(betaDir, "objects", betaTip[:2], betaTip[2:]))
	require.NoError(t, err)
	rec := getTap(t, h, "git.beta.test", "objects/"+betaTip[:2]+"/"+betaTip[2:])
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, want, rec.Body.Bytes())

	// Flipping back to alpha within the TTL serves alpha's own cached lineage
	recAlpha2 := getTap(t, h, "git.alpha.test", "info/refs")
	require.Equal(t, http.StatusOK, recAlpha2.Code)
	assert.Equal(t, recAlpha.Body.String(), recAlpha2.Body.String())
}

func TestServeTap_RejectsEscapingPaths(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "myapp", "binary-bytes")

	require.Equal(t, http.StatusOK, getTap(t, h, "git.example.com", "info/refs").Code)
	require.NoError(t, os.WriteFile(filepath.Join(h.DataDir, "secret.txt"), []byte("s"), 0o644))

	rec := getTap(t, h, "git.example.com", "../../secret.txt")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
