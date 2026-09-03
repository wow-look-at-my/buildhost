package brew

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func seedPrivateBrewProject(t *testing.T, d *db.DB, store *storage.Filesystem, name, body string) *db.Project {
	t.Helper()
	ctx := context.Background()

	proj := &db.Project{Name: name, IsPrivate: true, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))
	return proj
}

func withReadToken(req *http.Request, projectID *int64) *http.Request {
	tok := &db.APIToken{ID: 7, Name: "test", Scopes: "read", ProjectID: projectID}
	return req.WithContext(auth.WithToken(req.Context(), tok))
}

func tapFile(t *testing.T, serve http.HandlerFunc, urlPath, pathValue string, tokenProject *int64, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", urlPath, nil)
	req.Host = "brew.example.com"
	if pathValue != "" {
		req.SetPathValue("path", pathValue)
	}
	if authed {
		req = withReadToken(req, tokenProject)
	}
	rec := httptest.NewRecorder()
	serve(rec, req)
	return rec
}

func TestServePrivateTap_AnonymousGetsBasicChallenge(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	rec := tapFile(t, h.ServePrivateTap, "/private/tap.git/info/refs", "info/refs", nil, false)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	assert.NotContains(t, rec.Body.String(), "secretapp")
}

func TestServePrivateTap_TokenScopedTapIncludesPrivateFormula(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "ns/secretapp", "priv-binary")

	// A global read token sees public + private projects.
	refs := tapFile(t, h.ServePrivateTap, "/private/tap.git/info/refs", "info/refs", nil, true)
	require.Equal(t, http.StatusOK, refs.Code)
	assert.Equal(t, "private, no-store", refs.Header().Get("Cache-Control"))
	assert.Equal(t, "Authorization", refs.Header().Get("Vary"))

	req := withReadToken(httptest.NewRequest("GET", "/private/tap.git", nil), nil)
	req.Host = "brew.example.com"
	files, err := h.buildTapFiles(req)
	require.NoError(t, err)

	all := tapFilesText(files)
	// Private formula present under its folded name, using the token strategy.
	assert.Contains(t, all, "ns-secretapp.rb")
	assert.Contains(t, all, `require_relative "../lib/buildhost_private_download"`)
	assert.Contains(t, all, "using: BuildhostCurlDownloadStrategy")
	assert.Contains(t, all, "class NsSecretapp < Formula")
	// Slash-named projects install by BASENAME: the tar.gz's only top-level
	assert.Contains(t, all, `bin.install "secretapp"`)
	assert.NotContains(t, all, `bin.install "ns/secretapp"`)
	// The strategy library rides along and never embeds a token.
	assert.Contains(t, all, "buildhost_private_download.rb")
	assert.Contains(t, all, `ENV["HOMEBREW_BUILDHOST_TOKEN"]`)
	assert.Contains(t, all, "class BuildhostCurlDownloadStrategy < CurlDownloadStrategy")
}

func TestScopedTap_PublicFormulaStaysPlain(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")

	// Even through the authenticated tap, a PUBLIC project's formula keeps the
	req := withReadToken(httptest.NewRequest("GET", "/private/tap.git", nil), nil)
	req.Host = "brew.example.com"
	files, err := h.buildTapFiles(req)
	require.NoError(t, err)

	all := tapFilesText(files)
	assert.Contains(t, all, "class Pubapp < Formula")
	assert.NotContains(t, all, "using: BuildhostCurlDownloadStrategy\n      sha256")
	// The only require_relative in the tap text is inside no formula -- the
	assert.NotContains(t, all, "require_relative")
}

func TestScopedTap_ProjectScopedTokenCannotSeeOtherPrivateProjects(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	mine := seedPrivateBrewProject(t, d, store, "mine", "mine-binary")
	seedPrivateBrewProject(t, d, store, "other-secret", "other-binary")

	req := withReadToken(httptest.NewRequest("GET", "/private/tap.git", nil), &mine.ID)
	req.Host = "brew.example.com"
	files, err := h.buildTapFiles(req)
	require.NoError(t, err)

	all := tapFilesText(files)
	assert.Contains(t, all, "mine.rb")
	assert.Contains(t, all, "pubapp.rb")
	assert.NotContains(t, all, "other-secret")
}

func TestAnonymousTap_NeverContainsPrivateNames(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	req := httptest.NewRequest("GET", "/brew/tap.git", nil)
	req.Host = "git.example.com"
	files, err := h.buildTapFiles(req)
	require.NoError(t, err)

	all := tapFilesText(files)
	assert.Contains(t, all, "pubapp.rb")
	assert.NotContains(t, all, "secretapp")
	// The strategy library is part of the uniform tap layout even when nothing
	assert.Contains(t, all, "buildhost_private_download.rb")
}

func TestRedirectTap_AnonymousRedirects_AuthenticatedServedInPlace(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	anon := tapFile(t, h.RedirectTap, "/tap.git/info/refs", "info/refs", nil, false)
	require.Equal(t, http.StatusMovedPermanently, anon.Code)
	assert.Equal(t, "https://git.example.com/brew/tap.git/info/refs", anon.Header().Get("Location"))

	// A credentialed request must NOT be redirected: the client would drop the
	authed := tapFile(t, h.RedirectTap, "/tap.git/info/refs", "info/refs", nil, true)
	require.Equal(t, http.StatusOK, authed.Code)
	assert.Equal(t, "private, no-store", authed.Header().Get("Cache-Control"))
	assert.Equal(t, "Authorization", authed.Header().Get("Vary"))
	assert.Contains(t, authed.Body.String(), "refs/heads/main")
}

func TestTapSnapshots_KeyedByScopeWithoutThrashing(t *testing.T) {
	t.Serial()
	oldTTL := tapCacheTTL
	tapCacheTTL = time.Hour
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	anon1 := tapFile(t, h.ServeTap, "/brew/tap.git/info/refs", "info/refs", nil, false)
	require.Equal(t, http.StatusOK, anon1.Code)
	authed1 := tapFile(t, h.ServeTap, "/brew/tap.git/info/refs", "info/refs", nil, true)
	require.Equal(t, http.StatusOK, authed1.Code)

	assert.NotEqual(t, anon1.Body.String(), authed1.Body.String())

	// ...and revisiting each scope hits its own live snapshot: same bytes, no
	anon2 := tapFile(t, h.ServeTap, "/brew/tap.git/info/refs", "info/refs", nil, false)
	authed2 := tapFile(t, h.ServeTap, "/brew/tap.git/info/refs", "info/refs", nil, true)
	assert.Equal(t, anon1.Body.String(), anon2.Body.String())
	assert.Equal(t, authed1.Body.String(), authed2.Body.String())
	h.tapMu.Lock()
	assert.Len(t, h.tapSnaps, 2)
	h.tapMu.Unlock()
}

func TestServeFormula_PrivateProjectUsesTokenStrategyAndNoStore(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj := seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	req := httptest.NewRequest("GET", "/Formula/secretapp.rb", nil)
	req.Host = "brew.example.com"
	req = req.WithContext(auth.WithProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	body := rec.Body.String()
	assert.Contains(t, body, `require_relative "../lib/buildhost_private_download"`)
	assert.Contains(t, body, "using: BuildhostCurlDownloadStrategy")
	// The formula must never embed the caller's token; auth comes from
	assert.NotContains(t, body, "token=")
}

// tapFilesText concatenates every tap file path and its content, so tests can
// assert on tap contents (formula filenames live in the paths, formula text in
func tapFilesText(files map[string][]byte) string {
	var b strings.Builder
	for path, data := range files {
		b.WriteString(path)
		b.WriteByte('\n')
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// Guard the strategy source the tap ships: env var must be HOMEBREW_-prefixed
// (Homebrew scrubs everything else before formula code runs) and no token may
// be baked in.
func TestPrivateStrategySource(t *testing.T) {
	t.Serial()
	assert.Contains(t, repackage.BrewPrivateStrategy, `ENV["HOMEBREW_BUILDHOST_TOKEN"]`)
	assert.Contains(t, repackage.BrewPrivateStrategy, "Authorization: Bearer ")
	assert.Equal(t, "lib/buildhost_private_download.rb", repackage.BrewPrivateStrategyPath)
}

// A digit-leading project name is structurally unloadable by Homebrew, and
// emitting `class 7zip < Formula` is a guaranteed ".rb: syntax error" that
func TestHostileProjectNames_NeverEmitInvalidRuby(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	proj, _, _ := seedBrewProject(t, d, store, "7zip", "digit-binary")
	seedBrewProject(t, d, store, "dotted.app", "dotted-binary")

	req := httptest.NewRequest("GET", "/Formula/7zip.rb", nil)
	req.Host = "brew.example.com"
	req = req.WithContext(auth.WithProject(req.Context(), proj))
	rec := httptest.NewRecorder()
	h.ServeFormula(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	tapReq := httptest.NewRequest("GET", "/brew/tap.git", nil)
	tapReq.Host = "git.example.com"
	files, err := h.buildTapFiles(tapReq)
	require.NoError(t, err)
	all := tapFilesText(files)
	assert.NotContains(t, all, "7zip", "digit-leading project must be excluded from the tap")
	// The dotted project stays, with a class name brew both parses and loads.
	assert.Contains(t, all, "dotted.app.rb")
	assert.Contains(t, all, "class DottedApp < Formula")
	assert.NotContains(t, all, "class Dotted.app")
}
