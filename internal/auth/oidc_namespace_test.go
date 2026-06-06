package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/router"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover slash-namespaced ("<repo>/<binary>") projects: the OIDC
// namespace authorization added to requireProject, plus an end-to-end proof
// that the router resolves a multi-segment {project} path value.

func TestOIDCAuthorizesProject(t *testing.T) {
	tests := []struct {
		oidc, requested	string
		want		bool
	}{
		// A repo authorizes itself and any slash-namespaced project beneath it.
		{"log-streamer", "log-streamer", true},
		{"log-streamer", "log-streamer/client", true},
		{"log-streamer", "log-streamer/server", true},
		{"log-streamer", "log-streamer/a/b/c", true},
		{"foo", "foo/cli", true},
		// The trailing "/" boundary keeps sibling prefixes out.
		{"log-streamer", "log-streamer-evil", false},
		{"log-streamer", "log-streamerx", false},
		{"log-streamer", "other", false},
		{"log-streamer", "other/log-streamer", false},
		// An empty OIDC identity authorizes nothing.
		{"", "log-streamer", false},
		{"", "", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, oidcAuthorizesProject(tc.oidc, tc.requested),
			"oidcAuthorizesProject(%q, %q)", tc.oidc, tc.requested)
	}
}

func TestValidNamespacedProjectName(t *testing.T) {
	for _, s := range []string{"foo", "foo/cli", "log-streamer/client", "a/b/c", "a1.2_3-4/b5"} {
		assert.True(t, validNamespacedProjectName(s), "expected valid: %q", s)
	}
	for _, s := range []string{"", "/foo", "foo/", "foo//bar", "Foo", "foo/Bar", "-foo", "foo/.bar", "foo bar"} {
		assert.False(t, validNamespacedProjectName(s), "expected invalid: %q", s)
	}
}

// TestRequireProject_OIDCNamespace_AutoCreatesSubProject proves a GitHub
// Actions OIDC token for repo "log-streamer" may auto-provision and write to
// the per-binary project "log-streamer/client".
func TestRequireProject_OIDCNamespace_AutoCreatesSubProject(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "log-streamer/client", access: WriteAccess}
	}
	var gotProject *db.Project
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithOIDCProject(WithToken(context.Background(), tok), "log-streamer")
	req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "log-streamer/client", gotProject.Name)

	created, err := d.GetProject(context.Background(), "log-streamer/client")
	require.NoError(t, err)
	assert.Equal(t, "log-streamer/client", created.Name)
}

// TestRequireProject_OIDCNamespace_RejectsOutsideNamespace proves a repo's OIDC
// token cannot reach a sibling-prefixed or unrelated project, and never
// auto-creates one.
func TestRequireProject_OIDCNamespace_RejectsOutsideNamespace(t *testing.T) {
	for _, requested := range []string{"log-streamer-evil", "log-streamerx", "other/thing"} {
		d := openTestDB(t)
		initTestMiddleware(t, d)

		parse := func(r *http.Request) RouteInfo {
			return testRouteInfo{project: requested, access: WriteAccess}
		}
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("handler must not run for %q", requested)
		})
		handler := requireProjectFunc(parse, inner)

		tok := &db.APIToken{ID: -1, Scopes: "read,write"}
		ctx := WithOIDCProject(WithToken(context.Background(), tok), "log-streamer")
		req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, "requested=%s", requested)
		_, err := d.GetProject(context.Background(), requested)
		assert.Error(t, err, "must not auto-create %q", requested)
	}
}

// TestRequireProject_OIDCNamespace_WritesExistingSubProject proves the same
// token can write to an already-existing project in its namespace.
func TestRequireProject_OIDCNamespace_WritesExistingSubProject(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	require.NoError(t, d.CreateProject(context.Background(),
		&db.Project{Name: "log-streamer/server", Versioning: "auto"}))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "log-streamer/server", access: WriteAccess}
	}
	var called bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := requireProjectFunc(parse, inner)

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithOIDCProject(WithToken(context.Background(), tok), "log-streamer")
	req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestRequireProject_MultiSegmentRouting_EndToEnd drives the real router the
// registry uses, proving a slash-namespaced project survives path matching:
// {project} greedily captures "log-streamer/client" while the trailing literal
// "releases"/"artifacts" anchors let {version}/{os}/{arch} bind correctly. The
// OIDC token for repo "log-streamer" then auto-provisions it.
func TestRequireProject_MultiSegmentRouting_EndToEnd(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: r.PathValue("project"), access: WriteAccess}
	}
	var gotProject *db.Project
	var gotVersion, gotOS, gotArch string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProject = ProjectFrom(r.Context())
		gotVersion = r.PathValue("version")
		gotOS = r.PathValue("os")
		gotArch = r.PathValue("arch")
		w.WriteHeader(http.StatusOK)
	})

	rt := router.New()
	rt.HandleFunc("PUT /api/v1/projects/{project}/releases/{version}/artifacts/{os}/{arch}",
		router.Allow, requireProjectFunc(parse, inner))

	tok := &db.APIToken{ID: -1, Scopes: "read,write"}
	ctx := WithOIDCProject(WithToken(context.Background(), tok), "log-streamer")
	req := httptest.NewRequest("PUT",
		"/api/v1/projects/log-streamer/client/releases/1.0.0/artifacts/linux/amd64", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, gotProject)
	assert.Equal(t, "log-streamer/client", gotProject.Name)
	assert.Equal(t, "1.0.0", gotVersion)
	assert.Equal(t, "linux", gotOS)
	assert.Equal(t, "amd64", gotArch)

	created, err := d.GetProject(context.Background(), "log-streamer/client")
	require.NoError(t, err)
	assert.Equal(t, "log-streamer/client", created.Name)
}
