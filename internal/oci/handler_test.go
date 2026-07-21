package oci

import (
	"bytes"
	"context"
	"io"
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

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store, Gen: repackage.NewGenerator(store, d, t.TempDir())}
	h.uploads = newUploadStore(filepath.Join(t.TempDir(), "oci-uploads"), 10<<30)
	return h, d, store
}

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *db.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

func publishWithOCI(t *testing.T, ctx context.Context, d *db.DB, store *storage.Filesystem, proj *db.Project, version string, versionNum int64) *db.Release {
	t.Helper()

	rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: versionNum, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))

	binaryData := "#!/bin/sh\necho hello"
	key, size, err := store.Put(ctx, strings.NewReader(binaryData))
	require.NoError(t, err)

	a := &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}
	require.NoError(t, d.CreateArtifact(ctx, a))

	oci := &repackage.OCI{Store: store, DB: d}
	data, err := readAll(store, ctx, key)
	require.NoError(t, err)

	out, err := oci.Repackage(ctx, repackage.Input{
		Project:  *proj,
		Release:  *rel,
		Artifact: *a,
		Reader:   bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	require.NoError(t, err)
	defer out.Reader.Close()

	manifestKey, manifestSize, err := store.Put(ctx, out.Reader)
	require.NoError(t, err)
	require.NoError(t, d.CreatePackagedArtifact(ctx, a.ID, "oci", manifestKey, manifestSize, manifestKey, out.Filename, "{}"))

	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	return rel
}

// publishMultiArch publishes a release with two binary artifacts (amd64, arm64)
// and -- deliberately -- does NOT pre-store any OCI manifest. This is the
// production shape for a synthesized multi-arch image: the OCI serve path must
// generate, persist and link each platform's child manifest itself. (Contrast
// publishWithOCI, which pre-persists the manifest and would mask the
// dangling-index bug.)
func publishMultiArch(t *testing.T, ctx context.Context, d *db.DB, store *storage.Filesystem, proj *db.Project, version string, versionNum int64) *db.Release {
	t.Helper()

	rel := &db.Release{ProjectID: proj.ID, Version: version, VersionNum: versionNum, GitBranch: db.LatestBranch}
	require.NoError(t, d.CreateRelease(ctx, rel))

	for _, arch := range []db.Arch{db.ArchAMD64, db.ArchARM64} {
		key, size, err := store.Put(ctx, strings.NewReader("#!/bin/sh\necho hello "+string(arch)))
		require.NoError(t, err)
		require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
			ReleaseID: rel.ID, OS: db.OSLinux, Arch: arch,
			Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
		}))
	}

	require.NoError(t, d.PublishRelease(ctx, rel.ID))
	return rel
}

func readAll(store *storage.Filesystem, ctx context.Context, key string) ([]byte, error) {
	rc, _, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	return data, err
}

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name string
		path string
		want route
	}{
		{
			name: "manifest, single-segment name",
			path: "/v2/buildhost/manifests/latest",
			want: route{project: "buildhost", action: "manifests", reference: "latest"},
		},
		{
			name: "manifest, dashed name",
			path: "/v2/go-toolchain/manifests/latest",
			want: route{project: "go-toolchain", action: "manifests", reference: "latest"},
		},
		{
			name: "blob, single-segment name",
			path: "/v2/buildhost/blobs/sha256:abc",
			want: route{project: "buildhost", action: "blobs", reference: "sha256:abc"},
		},
		{
			name: "manifest, multi-segment name (decoded path with literal '/')",
			path: "/v2/library/foo/manifests/latest",
			want: route{project: "library/foo", action: "manifests", reference: "latest"},
		},
		{
			name: "manifest, deeply nested multi-segment name",
			path: "/v2/team/group/proj-name/manifests/v1",
			want: route{project: "team/group/proj-name", action: "manifests", reference: "v1"},
		},
		{
			name: "blob, multi-segment name",
			path: "/v2/library/foo/blobs/sha256:def",
			want: route{project: "library/foo", action: "blobs", reference: "sha256:def"},
		},
		{
			name: "name itself contains literal 'manifests' segment, distinguished by LastIndex",
			path: "/v2/foo/manifests/bar/manifests/latest",
			want: route{project: "foo/manifests/bar", action: "manifests", reference: "latest"},
		},
		{
			name: "tags listing, multi-segment name",
			path: "/v2/library/foo/tags/list",
			want: route{project: "library/foo", action: "tags", reference: "list"},
		},
		{
			name: "bare project, single-segment",
			path: "/v2/myapp",
			want: route{project: "myapp"},
		},
		{
			name: "action only, no reference, single-segment",
			path: "/v2/myapp/manifests",
			want: route{project: "myapp", action: "manifests"},
		},
		{
			name: "action only, no reference, multi-segment name",
			path: "/v2/library/foo/manifests",
			want: route{project: "library/foo", action: "manifests"},
		},
		{
			name: "name itself contains an action keyword as final segment",
			path: "/v2/foo/manifests/blobs/sha256:abc",
			want: route{project: "foo/manifests", action: "blobs", reference: "sha256:abc"},
		},
		{
			name: "blob upload start (no uuid)",
			path: "/v2/myapp/blobs/uploads/",
			want: route{project: "myapp", action: "uploads"},
		},
		{
			name: "blob upload chunk by uuid, multi-segment name",
			path: "/v2/library/foo/blobs/uploads/upload-123",
			want: route{project: "library/foo", action: "uploads", reference: "upload-123"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// parseOCIPath is the pure path parser; parseRoute just trims the
			// /v2/ prefix and stamps the HTTP method onto the result.
			got := parseOCIPath(strings.TrimPrefix(tt.path, "/v2/"))
			assert.Equal(t, tt.want, got)
		})
	}
}

// An unauthenticated GET /v2/ must 401 with a Basic challenge so the Docker/OCI
// client knows credentials are required (the auth-discovery handshake). A 200
// here makes clients conclude no auth is needed and the subsequent manifest pull
// 401s, killing the pull.
func TestV2Root_Unauthenticated(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/", nil)
	rec := httptest.NewRecorder()
	h.V2Root(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
	assert.Equal(t, "registry/2.0", rec.Header().Get("Docker-Distribution-API-Version"))
	assert.Contains(t, rec.Body.String(), `"code":"UNAUTHORIZED"`)
}

func TestV2Root_HEAD_Unauthenticated(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("HEAD", "/v2/", nil)
	rec := httptest.NewRecorder()
	h.V2Root(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"))
	assert.Equal(t, "registry/2.0", rec.Header().Get("Docker-Distribution-API-Version"))
}

// Once a valid credential is presented (the auth middleware puts the token in
// the context), /v2/ returns the 200 base response.
func TestV2Root_Authenticated(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("GET", "/v2/", nil)
	req = req.WithContext(auth.WithToken(req.Context(), &db.APIToken{}))
	rec := httptest.NewRecorder()
	h.V2Root(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "registry/2.0", rec.Header().Get("Docker-Distribution-API-Version"))
	assert.Equal(t, "{}\n", rec.Body.String())
	assert.Empty(t, rec.Header().Get("Www-Authenticate"))
}

func TestV2Root_HEAD_Authenticated(t *testing.T) {
	h, _, _ := setupTest(t)

	req := httptest.NewRequest("HEAD", "/v2/", nil)
	req = req.WithContext(auth.WithToken(req.Context(), &db.APIToken{}))
	rec := httptest.NewRecorder()
	h.V2Root(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "registry/2.0", rec.Header().Get("Docker-Distribution-API-Version"))
}

func TestServeHTTP_UnknownAction(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/v2/myapp/unknown/foo", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "unknown", reference: "foo"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `"code":"NAME_UNKNOWN"`)
}
