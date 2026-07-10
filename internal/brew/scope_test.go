package brew

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
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

// seedPrivateBrewProject creates a PRIVATE project with one published release
// and one linux/amd64 binary artifact.
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

// tapFile fetches one path of the tap through the given entrypoint with an
// optional token in context.
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
	repo, err := h.buildTapRepo(req)
	require.NoError(t, err)

	all := tapRepoText(repo)
	// Private formula present under its folded name, using the token strategy.
	assert.Contains(t, all, "ns-secretapp.rb")
	assert.Contains(t, all, `require_relative "../lib/buildhost_private_download"`)
	assert.Contains(t, all, "using: BuildhostCurlDownloadStrategy")
	assert.Contains(t, all, "class NsSecretapp < Formula")
	// Slash-named projects install by BASENAME: the tar.gz's only top-level
	// entry is the namespace directory, and brew strips a lone top-level dir
	// when unpacking, so the staged file is just the basename. Installing the
	// slashed path ENOENTs (reproduced against real brew 6.0.9).
	assert.Contains(t, all, `bin.install "secretapp"`)
	assert.NotContains(t, all, `bin.install "ns/secretapp"`)
	// The strategy library rides along and never embeds a token.
	assert.Contains(t, all, "buildhost_private_download.rb")
	assert.Contains(t, all, `ENV["HOMEBREW_BUILDHOST_TOKEN"]`)
	assert.Contains(t, all, "class BuildhostCurlDownloadStrategy < CurlDownloadStrategy")
}

func TestScopedTap_PublicFormulaStaysPlain(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")

	// Even through the authenticated tap, a PUBLIC project's formula keeps the
	// plain anonymous-download shape (no strategy, no require).
	req := withReadToken(httptest.NewRequest("GET", "/private/tap.git", nil), nil)
	req.Host = "brew.example.com"
	repo, err := h.buildTapRepo(req)
	require.NoError(t, err)

	all := tapRepoText(repo)
	assert.Contains(t, all, "class Pubapp < Formula")
	assert.NotContains(t, all, "using: BuildhostCurlDownloadStrategy\n      sha256")
	// The only require_relative in the tap text is inside no formula -- the
	// strategy file itself contains none.
	assert.NotContains(t, all, "require_relative")
}

func TestScopedTap_ProjectScopedTokenCannotSeeOtherPrivateProjects(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	mine := seedPrivateBrewProject(t, d, store, "mine", "mine-binary")
	seedPrivateBrewProject(t, d, store, "other-secret", "other-binary")

	req := withReadToken(httptest.NewRequest("GET", "/private/tap.git", nil), &mine.ID)
	req.Host = "brew.example.com"
	repo, err := h.buildTapRepo(req)
	require.NoError(t, err)

	all := tapRepoText(repo)
	assert.Contains(t, all, "mine.rb")
	assert.Contains(t, all, "pubapp.rb")
	assert.NotContains(t, all, "other-secret")
}

func TestAnonymousTap_NeverContainsPrivateNames(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	req := httptest.NewRequest("GET", "/brew/tap.git", nil)
	req.Host = "git.example.com"
	repo, err := h.buildTapRepo(req)
	require.NoError(t, err)

	all := tapRepoText(repo)
	assert.Contains(t, all, "pubapp.rb")
	assert.NotContains(t, all, "secretapp")
	// The strategy library is part of the uniform tap layout even when nothing
	// references it (it contains no secrets).
	assert.Contains(t, all, "buildhost_private_download.rb")
}

func TestRedirectTap_AnonymousRedirects_AuthenticatedServedInPlace(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "pubapp", "pub-binary")
	seedPrivateBrewProject(t, d, store, "secretapp", "priv-binary")

	anon := tapFile(t, h.RedirectTap, "/tap.git/info/refs", "info/refs", nil, false)
	require.Equal(t, http.StatusMovedPermanently, anon.Code)
	assert.Equal(t, "https://git.example.com/brew/tap.git/info/refs", anon.Header().Get("Location"))

	// A credentialed request must NOT be redirected: the client would drop the
	// credential on the cross-host follow and silently land on the public tap.
	authed := tapFile(t, h.RedirectTap, "/tap.git/info/refs", "info/refs", nil, true)
	require.Equal(t, http.StatusOK, authed.Code)
	assert.Equal(t, "private, no-store", authed.Header().Get("Cache-Control"))
	assert.Equal(t, "Authorization", authed.Header().Get("Vary"))
	assert.Contains(t, authed.Body.String(), "refs/heads/main")
}

func TestTapSnapshots_KeyedByScopeWithoutThrashing(t *testing.T) {
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

	// Different scopes serve different tap builds (the scoped one carries the
	// extra private formula, so the commits differ)...
	assert.NotEqual(t, anon1.Body.String(), authed1.Body.String())

	// ...and revisiting each scope hits its own live snapshot: same bytes, no
	// rebuild thrash between interleaved anonymous and authenticated clients.
	anon2 := tapFile(t, h.ServeTap, "/brew/tap.git/info/refs", "info/refs", nil, false)
	authed2 := tapFile(t, h.ServeTap, "/brew/tap.git/info/refs", "info/refs", nil, true)
	assert.Equal(t, anon1.Body.String(), anon2.Body.String())
	assert.Equal(t, authed1.Body.String(), authed2.Body.String())
	h.tapMu.Lock()
	assert.Len(t, h.tapSnaps, 2)
	h.tapMu.Unlock()
}

func TestServeFormula_PrivateProjectUsesTokenStrategyAndNoStore(t *testing.T) {
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
	// HOMEBREW_BUILDHOST_TOKEN at install time.
	assert.NotContains(t, body, "token=")
}

// tapRepoText concatenates every decompressed loose object plus the plain-text
// repo files, so tests can assert on tap contents regardless of which object a
// string lives in. Loose objects are zlib-compressed; a name only ever appears
// inside tree objects and a file's content inside its blob, so asserting on
// the concatenation covers both.
func tapRepoText(repo map[string][]byte) string {
	var b strings.Builder
	for path, data := range repo {
		b.WriteString(path)
		b.WriteByte('\n')
		if strings.HasPrefix(path, "objects/") && len(data) > 0 {
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err == nil {
				raw, err := io.ReadAll(zr)
				zr.Close()
				if err == nil {
					b.Write(raw)
					b.WriteByte('\n')
					continue
				}
			}
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}

// Guard the strategy source the tap ships: env var must be HOMEBREW_-prefixed
// (Homebrew scrubs everything else before formula code runs) and no token may
// be baked in.
func TestPrivateStrategySource(t *testing.T) {
	assert.Contains(t, repackage.BrewPrivateStrategy, `ENV["HOMEBREW_BUILDHOST_TOKEN"]`)
	assert.Contains(t, repackage.BrewPrivateStrategy, "Authorization: Bearer ")
	assert.Equal(t, "lib/buildhost_private_download.rb", repackage.BrewPrivateStrategyPath)
}
