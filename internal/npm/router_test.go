package npm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// The npm endpoint is always exercised through the real router
// (auth.ServeHTTP): subdomain dispatch -> route match -> parseRoute/
// parseTarballRoute -> requireProject middleware -> handler. Handlers are
// never called directly with a hand-built context, so a missing/misparsed
// route or a broken slash-namespace round-trip is caught here rather than
// slipping through.
//
// auth.Init wires the package-global handler and middleware to a shared test
// DB/store exactly once (it mutates process-global router state); each test
// seeds uniquely named projects into that shared DB.

var (
	routerOnce    sync.Once
	routerDB      *db.DB
	routerStore   storage.Storage
	routerHandler http.Handler
)

func mustTempDir() string {
	d, err := os.MkdirTemp("", "npm-router-*")
	if err != nil {
		panic(err)
	}
	return d
}

func routerEnv(t *testing.T) (*db.DB, storage.Storage) {
	t.Helper()
	routerOnce.Do(func() {
		d, err := db.Open(filepath.Join(mustTempDir(), "npm-router.db"))
		require.NoError(t, err)
		store, err := storage.NewFilesystem(mustTempDir(), true)
		require.NoError(t, err)
		auth.Init(d, store, "http://localhost", mustTempDir(), nil, nil, nil, nil)
		// Wrap with the same token-authentication middleware the server applies
		// (server.Handler does auth.GetMiddleware().Authenticate(auth.ServeHTTP)),
		// so Bearer tokens are resolved before requireProject runs.
		routerHandler = auth.GetMiddleware().Authenticate(http.HandlerFunc(auth.ServeHTTP))
		routerDB, routerStore = d, store
	})
	return routerDB, routerStore
}

func npmGet(t *testing.T, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	routerEnv(t) // ensure routerHandler is wired
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "npm.localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	routerHandler.ServeHTTP(rec, req)
	return rec
}

// seedNPMPackage creates a published release with a single npm-package
// artifact (os=any, arch=any) and returns the stored tarball content.
func seedNPMPackage(t *testing.T, project, version, content string) {
	t.Helper()
	d, store := routerEnv(t)
	ctx := context.Background()
	proj := &db.Project{Name: project, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	key, size, err := store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: "any", Arch: "any",
		Kind: db.KindNPMPackage, StorageKey: key, Size: size, SHA256: key,
	}))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))
}

func decodePackument(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	return info
}

func TestRouter_Packument_NPMPackageArtifact(t *testing.T) {
	seedNPMPackage(t, "router-pkg", "5.0.0", "tarball")

	rec := npmGet(t, "", "/@buildhost/router-pkg")
	require.Equal(t, http.StatusOK, rec.Code)
	info := decodePackument(t, rec)
	assert.Equal(t, "@buildhost/router-pkg", info["name"])

	v := info["versions"].(map[string]any)["5.0.0"].(map[string]any)
	dist := v["dist"].(map[string]any)
	assert.Contains(t, dist["tarball"].(string), "/@buildhost/router-pkg/-/router-pkg-5.0.0.tgz")
	_, hasBin := v["bin"]
	assert.False(t, hasBin, "npm-package should not carry a bin wrapper")
	_, hasOptDeps := v["optionalDependencies"]
	assert.False(t, hasOptDeps)
}

func TestRouter_Tarball_Success(t *testing.T) {
	content := "fake npm tarball content"
	seedNPMPackage(t, "router-tarball", "1.0.0", content)

	rec := npmGet(t, "", "/@buildhost/router-tarball/-/router-tarball-1.0.0.tgz")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/gzip", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "router-tarball-1.0.0.tgz")
	assert.Equal(t, content, rec.Body.String())
}

func TestRouter_Tarball_NotFound(t *testing.T) {
	seedNPMPackage(t, "router-nf", "1.0.0", "content")

	// Bad filenames (wrong prefix, no version) and a missing release all 404.
	for _, p := range []string{
		"/@buildhost/router-nf/-/other-1.0.0.tgz",
		"/@buildhost/router-nf/-/router-nf.tgz",
		"/@buildhost/router-nf/-/router-nf-.tgz",
		"/@buildhost/router-nf/-/router-nf-9.9.9.tgz",
	} {
		rec := npmGet(t, "", p)
		assert.Equal(t, http.StatusNotFound, rec.Code, "path=%q", p)
	}
}

// TestRouter_NamespacedProject proves a slash-namespaced buildhost project
// round-trips as a single-slash (valid) npm package: project
// "cc-marketplace/my-plugin" is served as "@buildhost/cc-marketplace__my-plugin"
// and its tarball downloads through the encoded route.
func TestRouter_NamespacedProject(t *testing.T) {
	content := "namespaced tarball"
	seedNPMPackage(t, "cc-marketplace/my-plugin", "2.0.0", content)

	// Packument: npm name encodes the namespace slash as "__".
	rec := npmGet(t, "", "/@buildhost/cc-marketplace__my-plugin")
	require.Equal(t, http.StatusOK, rec.Code)
	info := decodePackument(t, rec)
	assert.Equal(t, "@buildhost/cc-marketplace__my-plugin", info["name"])
	v := info["versions"].(map[string]any)["2.0.0"].(map[string]any)
	tarball := v["dist"].(map[string]any)["tarball"].(string)
	assert.Contains(t, tarball, "/@buildhost/cc-marketplace__my-plugin/-/cc-marketplace__my-plugin-2.0.0.tgz")

	// Tarball downloads via the encoded route and decodes back to the project.
	rec = npmGet(t, "", "/@buildhost/cc-marketplace__my-plugin/-/cc-marketplace__my-plugin-2.0.0.tgz")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, content, rec.Body.String())
}

func TestRouter_Packument_BinaryRepackage(t *testing.T) {
	d, store := routerEnv(t)
	ctx := context.Background()

	proj := &db.Project{Name: "router-bin", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	for _, plat := range []struct {
		os   db.OS
		arch db.Arch
	}{{db.OSLinux, db.ArchAMD64}, {db.OSDarwin, db.ArchARM64}} {
		key, size, err := store.Put(ctx, strings.NewReader("bin-"+string(plat.os)))
		require.NoError(t, err)
		require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
			ReleaseID: rel.ID, OS: plat.os, Arch: plat.arch,
			Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
		}))
	}
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	rec := npmGet(t, "", "/@buildhost/router-bin")
	require.Equal(t, http.StatusOK, rec.Code)
	info := decodePackument(t, rec)
	v := info["versions"].(map[string]any)["6.0.0"].(map[string]any)
	optDeps := v["optionalDependencies"].(map[string]any)
	assert.Contains(t, optDeps, "@buildhost/router-bin-linux-x64")
	assert.Contains(t, optDeps, "@buildhost/router-bin-darwin-arm64")
	assert.Equal(t, "./bin/run.js", v["bin"].(map[string]any)["router-bin"])
	assert.Contains(t, v["dist"].(map[string]any)["tarball"].(string), "fmt=npm-wrapper")

	// Platform sub-package packument.
	rec = npmGet(t, "", "/@buildhost/router-bin-linux-x64")
	require.Equal(t, http.StatusOK, rec.Code)
	info = decodePackument(t, rec)
	assert.Equal(t, "@buildhost/router-bin-linux-x64", info["name"])
	pv := info["versions"].(map[string]any)["6.0.0"].(map[string]any)
	assert.Equal(t, []any{"linux"}, pv["os"])
	assert.Equal(t, []any{"x64"}, pv["cpu"])

	// Unknown platform suffix on an existing project -> 404.
	rec = npmGet(t, "", "/@buildhost/router-bin-win32-ia32")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRouter_UnpublishedSkipped(t *testing.T) {
	d, _ := routerEnv(t)
	ctx := context.Background()
	proj := &db.Project{Name: "router-unpub", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	require.NoError(t, d.CreateRelease(ctx, &db.Release{ProjectID: proj.ID, Version: "1.0.0-rc1", VersionNum: 1}))

	rec := npmGet(t, "", "/@buildhost/router-unpub")
	require.Equal(t, http.StatusOK, rec.Code)
	info := decodePackument(t, rec)
	assert.Empty(t, info["versions"].(map[string]any))
}

func TestRouter_PrivateProject_RequiresAuth(t *testing.T) {
	d, _ := routerEnv(t)
	ctx := context.Background()

	token, _, err := d.CreateToken(ctx, "npm-router-test", nil, "read,write")
	require.NoError(t, err)

	proj := &db.Project{Name: "router-secret", IsPrivate: true, Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	// Unauthenticated request is rejected by requireProject before the handler.
	rec := npmGet(t, "", "/@buildhost/router-secret")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Authenticated request succeeds.
	rec = npmGet(t, token, "/@buildhost/router-secret")
	require.Equal(t, http.StatusOK, rec.Code)
	info := decodePackument(t, rec)
	assert.Equal(t, "@buildhost/router-secret", info["name"])
}
