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

// The first build after a publish leaves out versions whose digests are not
// cached and fills them in the background; the next build carries one
// keg-only name@version formula per published default-branch release.
func TestTap_VersionedFormulasPerDefaultBranchRelease(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "ns/app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")
	addRelease(t, d, store, proj, "9.9.9", 9009009, "feature-x", "feature-binary")

	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	assert.Contains(t, files, "Formula/ns-app.rb")
	assert.Contains(t, files, "Formula/ns-app/ns-app@1.1.0.rb")
	assert.NotContains(t, files, "Formula/ns-app/ns-app@1.0.0.rb")

	h.fillWG.Wait()
	files, err = h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)

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

	_, err := h.buildTapFiles(tapRequest(true))
	require.NoError(t, err)
	h.fillWG.Wait()

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

	_, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)
	h.fillWG.Wait()
	files, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)

	assert.Contains(t, files, "Formula/app/app@2.0.0-RC.rb")
	assert.NotContains(t, files, "Formula/app/app@2.0.0-rc.rb")
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

	require.Equal(t, http.StatusOK, mustGet(t, ts.URL+"/brew/tap.git/info/refs"))
	h.fillWG.Wait()

	dir := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", ts.URL+"/brew/tap.git", dir)
	runGit(t, dir, "fsck", "--strict")

	tree := runGit(t, dir, "ls-tree", "-r", "--name-only", "HEAD")
	assert.Contains(t, tree, "Formula/ns-app.rb\n")
	assert.Contains(t, tree, "Formula/ns-app/ns-app@1.0.0.rb\n")
	assert.Contains(t, tree, "Formula/ns-app/ns-app@1.1.0.rb\n")
	assert.Contains(t, tree, "lib/buildhost_private_download.rb\n")
}
