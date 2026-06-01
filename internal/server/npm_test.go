package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// These tests drive the npm registry endpoint through the real router
// (auth.ServeHTTP via httptest), exercising subdomain dispatch, route
// pattern matching, parseRoute/parseTarballRoute and the requireProject
// auth middleware end to end. Fixtures are seeded directly into the DB and
// store; only the serving path is under test.

func seedNpmPackage(t *testing.T, env *testEnv, project, version, content string) *db.Project {
	t.Helper()
	ctx := context.Background()
	proj := &db.Project{Name: project, Versioning: db.VersioningSemver}
	require.NoError(t, env.database.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: 1000000}
	require.NoError(t, env.database.CreateRelease(ctx, rel))
	key, size, err := env.store.Put(ctx, strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, env.database.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: "any", Arch: "any",
		Kind: db.KindNPMPackage, StorageKey: key, Size: size, SHA256: key,
	}))
	require.NoError(t, env.database.PublishRelease(ctx, rel.ID))
	return proj
}

func TestNPM_Packument_NPMPackageArtifact(t *testing.T) {
	env := setup(t)
	seedNpmPackage(t, env, "my-plugin", "5.0.0", "tarball-bytes")

	resp := env.getSubdomain(t, "npm", "/@buildhost/my-plugin")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var info map[string]any
	decodeJSON(t, resp, &info)
	assert.Equal(t, "@buildhost/my-plugin", info["name"])

	versions := info["versions"].(map[string]any)
	v := versions["5.0.0"].(map[string]any)
	dist := v["dist"].(map[string]any)
	// Tarball URL points at the npm subdomain tarball route.
	assert.Contains(t, dist["tarball"].(string), "/@buildhost/my-plugin/-/my-plugin-5.0.0.tgz")
	// npm-package artifacts are served verbatim: no bin wrapper / optionalDependencies.
	_, hasBin := v["bin"]
	assert.False(t, hasBin)
	_, hasOptDeps := v["optionalDependencies"]
	assert.False(t, hasOptDeps)
}

func TestNPM_Tarball_Success(t *testing.T) {
	env := setup(t)
	content := "fake npm tarball content"
	seedNpmPackage(t, env, "my-plugin", "1.0.0", content)

	resp := env.getSubdomain(t, "npm", "/@buildhost/my-plugin/-/my-plugin-1.0.0.tgz")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/gzip", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "my-plugin-1.0.0.tgz")
	assert.Equal(t, content, string(readBody(t, resp)))
}

func TestNPM_Tarball_BadFilename(t *testing.T) {
	env := setup(t)
	seedNpmPackage(t, env, "my-plugin", "1.0.0", "content")

	// Filename whose prefix doesn't match the project name, or has no version.
	for _, fn := range []string{"other-plugin-1.0.0.tgz", "my-plugin.tgz", "my-plugin-.tgz"} {
		resp := env.getSubdomain(t, "npm", "/@buildhost/my-plugin/-/"+fn)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, "filename=%q", fn)
		resp.Body.Close()
	}
}

func TestNPM_Tarball_ReleaseNotFound(t *testing.T) {
	env := setup(t)
	seedNpmPackage(t, env, "my-plugin", "1.0.0", "content")

	resp := env.getSubdomain(t, "npm", "/@buildhost/my-plugin/-/my-plugin-9.9.9.tgz")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestNPM_Packument_BinaryRepackage(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, env.database.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, env.database.CreateRelease(ctx, rel))
	for _, plat := range []struct {
		os   db.OS
		arch db.Arch
	}{{db.OSLinux, db.ArchAMD64}, {db.OSDarwin, db.ArchARM64}} {
		key, size, err := env.store.Put(ctx, strings.NewReader("bin-"+string(plat.os)))
		require.NoError(t, err)
		require.NoError(t, env.database.CreateArtifact(ctx, &db.Artifact{
			ReleaseID: rel.ID, OS: plat.os, Arch: plat.arch,
			Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
		}))
	}
	require.NoError(t, env.database.PublishRelease(ctx, rel.ID))

	resp := env.getSubdomain(t, "npm", "/@buildhost/go-toolchain")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var info map[string]any
	decodeJSON(t, resp, &info)
	versions := info["versions"].(map[string]any)
	v := versions["6.0.0"].(map[string]any)

	optDeps := v["optionalDependencies"].(map[string]any)
	assert.Contains(t, optDeps, "@buildhost/go-toolchain-linux-x64")
	assert.Contains(t, optDeps, "@buildhost/go-toolchain-darwin-arm64")
	bin := v["bin"].(map[string]any)
	assert.Equal(t, "./bin/run.js", bin["go-toolchain"])
	dist := v["dist"].(map[string]any)
	assert.Contains(t, dist["tarball"].(string), "fmt=npm-wrapper")
}

func TestNPM_PlatformPackument(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	proj := &db.Project{Name: "go-toolchain", Versioning: db.VersioningSemver}
	require.NoError(t, env.database.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "6.0.0", VersionNum: 6000000}
	require.NoError(t, env.database.CreateRelease(ctx, rel))
	key, size, err := env.store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, env.database.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))
	require.NoError(t, env.database.PublishRelease(ctx, rel.ID))

	resp := env.getSubdomain(t, "npm", "/@buildhost/go-toolchain-linux-x64")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var info map[string]any
	decodeJSON(t, resp, &info)
	assert.Equal(t, "@buildhost/go-toolchain-linux-x64", info["name"])
	versions := info["versions"].(map[string]any)
	v := versions["6.0.0"].(map[string]any)
	assert.Equal(t, []any{"linux"}, v["os"])
	assert.Equal(t, []any{"x64"}, v["cpu"])

	// Unknown platform suffix on an existing project -> 404.
	resp = env.getSubdomain(t, "npm", "/@buildhost/go-toolchain-win32-ia32")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestNPM_Packument_UnpublishedSkipped(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp2", Versioning: db.VersioningSemver}
	require.NoError(t, env.database.CreateProject(ctx, proj))
	require.NoError(t, env.database.CreateRelease(ctx, &db.Release{ProjectID: proj.ID, Version: "1.0.0-rc1", VersionNum: 1}))

	resp := env.getSubdomain(t, "npm", "/@buildhost/myapp2")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info map[string]any
	decodeJSON(t, resp, &info)
	assert.Empty(t, info["versions"].(map[string]any))
}

func TestNPM_PrivateProject_RequiresAuth(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: db.VersioningSemver}
	require.NoError(t, env.database.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, env.database.CreateRelease(ctx, rel))
	require.NoError(t, env.database.PublishRelease(ctx, rel.ID))

	// Unauthenticated request to a private project is rejected by the
	// requireProject middleware (never reaches the handler).
	resp := env.getSubdomain(t, "npm", "/@buildhost/secret")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// Authenticated request succeeds.
	resp = env.authGetSubdomain(t, "npm", "/@buildhost/secret")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info map[string]any
	decodeJSON(t, resp, &info)
	assert.Equal(t, "@buildhost/secret", info["name"])
}
