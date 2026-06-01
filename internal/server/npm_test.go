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

// TestNPM drives the npm registry endpoint through the real router
// (httptest server -> auth.ServeHTTP), exercising subdomain dispatch, route
// pattern matching, parseRoute/parseTarballRoute and the requireProject
// auth middleware end to end. Fixtures are seeded directly into the DB and
// store; only the serving path is under test.
//
// All cases share one server (one setup) so the suite pays the APT signing
// RSA-4096 keygen cost in server.New() only once -- per-test setups blow the
// package test timeout. Each subtest uses a distinct project name.
func TestNPM(t *testing.T) {
	env := setup(t)

	seedNpmPackage := func(t *testing.T, project, version, content string) {
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
	}

	t.Run("packument for npm-package artifact", func(t *testing.T) {
		seedNpmPackage(t, "pkg-info", "5.0.0", "tarball-bytes")

		resp := env.getSubdomain(t, "npm", "/@buildhost/pkg-info")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var info map[string]any
		decodeJSON(t, resp, &info)
		assert.Equal(t, "@buildhost/pkg-info", info["name"])

		versions := info["versions"].(map[string]any)
		v := versions["5.0.0"].(map[string]any)
		dist := v["dist"].(map[string]any)
		assert.Contains(t, dist["tarball"].(string), "/@buildhost/pkg-info/-/pkg-info-5.0.0.tgz")
		// npm-package artifacts are served verbatim: no bin wrapper / optionalDependencies.
		_, hasBin := v["bin"]
		assert.False(t, hasBin)
		_, hasOptDeps := v["optionalDependencies"]
		assert.False(t, hasOptDeps)
	})

	t.Run("tarball download", func(t *testing.T) {
		content := "fake npm tarball content"
		seedNpmPackage(t, "tarball-ok", "1.0.0", content)

		resp := env.getSubdomain(t, "npm", "/@buildhost/tarball-ok/-/tarball-ok-1.0.0.tgz")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/gzip", resp.Header.Get("Content-Type"))
		assert.Contains(t, resp.Header.Get("Content-Disposition"), "tarball-ok-1.0.0.tgz")
		assert.Equal(t, content, string(readBody(t, resp)))
	})

	t.Run("tarball bad filename", func(t *testing.T) {
		seedNpmPackage(t, "tarball-bad", "1.0.0", "content")
		for _, fn := range []string{"other-plugin-1.0.0.tgz", "tarball-bad.tgz", "tarball-bad-.tgz"} {
			resp := env.getSubdomain(t, "npm", "/@buildhost/tarball-bad/-/"+fn)
			assert.Equal(t, http.StatusNotFound, resp.StatusCode, "filename=%q", fn)
			resp.Body.Close()
		}
	})

	t.Run("tarball release not found", func(t *testing.T) {
		seedNpmPackage(t, "tarball-missing", "1.0.0", "content")
		resp := env.getSubdomain(t, "npm", "/@buildhost/tarball-missing/-/tarball-missing-9.9.9.tgz")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("packument for binary repackage", func(t *testing.T) {
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
		v := info["versions"].(map[string]any)["6.0.0"].(map[string]any)
		optDeps := v["optionalDependencies"].(map[string]any)
		assert.Contains(t, optDeps, "@buildhost/go-toolchain-linux-x64")
		assert.Contains(t, optDeps, "@buildhost/go-toolchain-darwin-arm64")
		assert.Equal(t, "./bin/run.js", v["bin"].(map[string]any)["go-toolchain"])
		assert.Contains(t, v["dist"].(map[string]any)["tarball"].(string), "fmt=npm-wrapper")

		// Platform sub-package packument.
		resp = env.getSubdomain(t, "npm", "/@buildhost/go-toolchain-linux-x64")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		decodeJSON(t, resp, &info)
		assert.Equal(t, "@buildhost/go-toolchain-linux-x64", info["name"])
		pv := info["versions"].(map[string]any)["6.0.0"].(map[string]any)
		assert.Equal(t, []any{"linux"}, pv["os"])
		assert.Equal(t, []any{"x64"}, pv["cpu"])

		// Unknown platform suffix on an existing project -> 404.
		resp = env.getSubdomain(t, "npm", "/@buildhost/go-toolchain-win32-ia32")
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("unpublished release skipped", func(t *testing.T) {
		ctx := context.Background()
		proj := &db.Project{Name: "unpub", Versioning: db.VersioningSemver}
		require.NoError(t, env.database.CreateProject(ctx, proj))
		require.NoError(t, env.database.CreateRelease(ctx, &db.Release{ProjectID: proj.ID, Version: "1.0.0-rc1", VersionNum: 1}))

		resp := env.getSubdomain(t, "npm", "/@buildhost/unpub")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var info map[string]any
		decodeJSON(t, resp, &info)
		assert.Empty(t, info["versions"].(map[string]any))
	})

	t.Run("private project requires auth", func(t *testing.T) {
		ctx := context.Background()
		proj := &db.Project{Name: "secret", IsPrivate: true, Versioning: db.VersioningSemver}
		require.NoError(t, env.database.CreateProject(ctx, proj))
		rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
		require.NoError(t, env.database.CreateRelease(ctx, rel))
		require.NoError(t, env.database.PublishRelease(ctx, rel.ID))

		// Unauthenticated request is rejected by requireProject (never reaches the handler).
		resp := env.getSubdomain(t, "npm", "/@buildhost/secret")
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		resp.Body.Close()

		// Authenticated request succeeds.
		resp = env.authGetSubdomain(t, "npm", "/@buildhost/secret")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var info map[string]any
		decodeJSON(t, resp, &info)
		assert.Equal(t, "@buildhost/secret", info["name"])
	})
}
