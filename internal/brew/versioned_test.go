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
	"github.com/wow-look-at-my/buildhost/internal/repackage"
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

// The tap carries one formula per project, like any other tap. A pinned
// release in the tap is listed by brew as a formula of its own.
func TestTap_OneFormulaPerProject(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "ns/app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")
	seedPrivateBrewProject(t, d, store, "ns/secretapp", "priv-v1")

	for _, authed := range []bool{false, true} {
		files, err := h.buildTapFiles(tapRequest(authed))
		require.NoError(t, err)
		for path := range files {
			assert.NotContains(t, path, "@", "the tap must hold no versioned formula")
			if rest, ok := strings.CutPrefix(path, "Formula/"); ok {
				assert.NotContains(t, rest, "/")
			}
		}
		assert.Contains(t, string(files["Formula/ns-app.rb"]), `version "1.1.0"`)
	}
}

// A tap build hashes only the latest release. Hashing every past release
// repackaged the whole history and pinned the CPU on a first clone.
func TestTap_BuildDigestsLatestReleaseOnly(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, old, _ := seedBrewProject(t, d, store, "app", "v1-binary")
	addRelease(t, d, store, proj, "1.1.0", 1001000, db.LatestBranch, "v11-binary")

	_, err := h.buildTapFiles(tapRequest(false))
	require.NoError(t, err)

	ctx := context.Background()
	artifacts, err := d.ListArtifactsByPlatform(ctx, old.ID)
	require.NoError(t, err)
	require.NotEmpty(t, artifacts)
	for _, a := range artifacts {
		_, _, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, a.CacheFormat(string(repackage.FormatTarGZ)))
		assert.ErrorIs(t, err, db.ErrNotFound, "a past release must not be hashed by a tap build")
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
	assert.Contains(t, rec.Body.String(), `keg_only "it pins one release`)
	assert.Equal(t, `inline; filename="ns-app@1.0.0.rb"`, rec.Header().Get("Content-Disposition"))

	assert.Equal(t, http.StatusNotFound, serve("ns-app@3.0.0.rb").Code)

	latest := serve("ns-app.rb")
	require.Equal(t, http.StatusOK, latest.Code)
	assert.Contains(t, latest.Body.String(), `version "1.1.0"`)
	assert.NotContains(t, latest.Body.String(), "keg_only")
}

// A real git clone of a tap whose project has several releases holds only the
// formula, and the history passes fsck.
func TestSmartClone_NoVersionedFormulas(t *testing.T) {
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
	assert.Equal(t, "Formula/ns-app.rb\nlib/buildhost_private_download.rb\n", tree)
}
