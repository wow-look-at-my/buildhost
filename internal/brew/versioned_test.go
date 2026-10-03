package brew

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func addRelease(t *testing.T, d *db.DB, store *storage.Filesystem, proj *db.Project, version string, num int64, branch, body string) *db.Release {
	t.Helper()
	ctx := context.Background()
	rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: num, GitBranch: branch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	key, size, err := store.Put(ctx, strings.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))
	return rel
}

func tapRequest(authed bool) *http.Request {
	req := httptest.NewRequest("GET", "/tap.git", nil)
	req.Host = "brew.example.com"
	if authed {
		req = withReadToken(req, nil)
	}
	return req
}

// One keg-only name@version formula per published default-branch release,
// in the first build after the publish.
func TestTap_VersionedFormulasPerDefaultBranchRelease(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "ns/app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")
	addRelease(t, d, store, proj, "9.9.9", 9009009, "feature-x", "feature-binary")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Contains(t, files, "Formula/ns-app.rb")

	old := string(files["Formula/ns-app/ns-app@1.0.0.rb"])
	require.NotEmpty(t, old)
	assert.Contains(t, old, "class NsAppAT100 < Formula\n")
	assert.Contains(t, old, `version "1.0.0"`)
	assert.Contains(t, old, "keg_only :versioned_formula")
	assert.Contains(t, old, "v=1.0.0")
	assert.Contains(t, old, `bin.install "app"`)

	newer := string(files["Formula/ns-app/ns-app@1.1.0.rb"])
	assert.Contains(t, newer, "class NsAppAT110 < Formula\n")
	assert.Contains(t, newer, "v=1.1.0")

	latest := string(files["Formula/ns-app.rb"])
	assert.Contains(t, latest, `version "1.1.0"`)
	assert.NotContains(t, latest, "keg_only")

	// A feature-branch release is never the default branch's version.
	for path := range files {
		assert.NotContains(t, path, "9.9.9")
	}
}

// The versioned formulas of a private project ride the authenticated tap
// only, download through the token strategy, and require it from a couple
// of levels down.
func TestTap_PrivateVersionedFormulas(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj := seedPrivateBrewProject(t, d, store, "ns/secretapp", "priv-v1")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "priv-v11")

	files, err := h.buildTapFiles(tapRequest(true))
	require.NoError(t, err)
	body := string(files["Formula/ns-secretapp/ns-secretapp@1.0.0.rb"])
	require.NotEmpty(t, body)
	assert.True(t, strings.HasPrefix(body, `require_relative "../../lib/buildhost_private_download"`+"\n"))
	assert.Contains(t, body, "using: BuildhostCurlDownloadStrategy")
	assert.Contains(t, body, "class NsSecretappAT100 < Formula")

	anon, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.NotContains(t, tapFilesText(anon), "secretapp")
}

// Versions that differ only by case would collide in a clone on a
// case-insensitive filesystem; the newest one keeps the path.
func TestTap_CaseCollidingVersionsKeepNewest(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "app", "base")
	addRelease(t, d, store, proj, "2.0.0-rc", 2000000, db.LatestBranch, "lower")
	addRelease(t, d, store, proj, "2.0.0-RC", 2000001, db.LatestBranch, "upper")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)

	assert.Contains(t, files, "Formula/app/app@2.0.0-RC.rb")
	assert.NotContains(t, files, "Formula/app/app@2.0.0-rc.rb")
}

func withInlineDigestBudget(t *testing.T, n int) {
	old := tapInlineDigestBudget
	tapInlineDigestBudget = n
	t.Cleanup(func() { tapInlineDigestBudget = old })
}

// Past the inline budget a version is left out and filled in the background.
// When the filler drains it drops the live lineages, so the next fetch sees
// the version without waiting out tapCacheTTL.
func TestTap_VersionsPastBudgetFillInBackground(t *testing.T) {
	t.Serial()
	withInlineDigestBudget(t, 0)
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Contains(t, files, "Formula/app/app@1.1.0.rb")
	assert.NotContains(t, files, "Formula/app/app@1.0.0.rb")

	require.Equal(t, http.StatusOK, getTap(t, h, "git.example.com", "info/refs").Code)
	h.fillWG.Wait()
	h.tapMu.Lock()
	live := len(h.tapSnaps)
	h.tapMu.Unlock()
	assert.Zero(t, live, "a drained filler must drop the live lineages")

	files, err = h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Contains(t, files, "Formula/app/app@1.0.0.rb")
}

func TestBackfillVersionDigests_FillsHistory(t *testing.T) {
	t.Serial()
	withInlineDigestBudget(t, 0)
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")
	addRelease(t, d, store, proj, "1.2.0", 1002000, db.LatestBranch, "v12-binary")

	require.NoError(t, h.backfillVersionDigests(context.Background()))
	h.fillWG.Wait()

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		assert.Contains(t, files, "Formula/app/app@"+v+".rb")
	}
}

func TestServeFormula_Versioned(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "ns/app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")

	serve := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/Formula/"+path, nil)
		req.Host = "brew.example.com"
		req.SetPathValue("path", path)
		assert.Equal(t, "ns/app", h.parseRoute(req).ProjectName())
		req = req.WithContext(auth.WithProject(req.Context(), proj))
		rec := httptest.NewRecorder()
		h.ServeFormula(rec, req)
		return rec
	}

	rec := serve("ns-app@1.0.0.rb")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "class NsAppAT100 < Formula")
	assert.Contains(t, rec.Body.String(), "keg_only :versioned_formula")
	assert.Equal(t, `inline; filename="ns-app@1.0.0.rb"`, rec.Header().Get("Content-Disposition"))

	assert.Equal(t, http.StatusNotFound, serve("ns-app@3.0.0.rb").Code)

	latest := serve("ns-app.rb")
	require.Equal(t, http.StatusOK, latest.Code)
	assert.Contains(t, latest.Body.String(), `version "1.1.0"`)
	assert.NotContains(t, latest.Body.String(), "keg_only")
}

// A real git clone of a tap carrying versioned formulas: Formula/<name>/ is a
// nested tree, and the whole history passes fsck.
func TestSmartClone_VersionedFormulasAreNestedTrees(t *testing.T) {
	t.Serial()
	requireGit(t)

	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "ns/app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")
	ts := smartTapServer(t, h)

	dir := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", ts.URL+"/brew/tap.git", dir)
	runGit(t, dir, "fsck", "--strict")

	tree := runGit(t, dir, "ls-tree", "-r", "--name-only", "HEAD")
	assert.Contains(t, tree, "Formula/ns-app.rb\n")
	assert.Contains(t, tree, "Formula/ns-app/ns-app@1.0.0.rb\n")
	assert.Contains(t, tree, "Formula/ns-app/ns-app@1.1.0.rb\n")
	assert.Contains(t, tree, "lib/buildhost_private_download.rb\n")
}
