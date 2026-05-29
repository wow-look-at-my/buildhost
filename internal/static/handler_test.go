package static

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestCanonicalQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already sorted", "arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", "arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0"},
		{"unsorted", "v=1.0.0&id=myapp&os=linux&arch=amd64&fmt=raw", "arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0"},
		{"strips unknown", "arch=amd64&foo=bar&id=myapp&os=linux&v=1", "arch=amd64&id=myapp&os=linux&v=1"},
		{"keeps debug", "debug=1&id=myapp&v=1&os=linux&arch=amd64", "arch=amd64&debug=1&id=myapp&os=linux&v=1"},
		{"empty", "", ""},
		{"only unknown", "foo=bar&baz=qux", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, _ := url.ParseQuery(tt.input)
			got := canonicalQuery(q)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestURL(t *testing.T) {
	u := URL("https://example.com", For("myapp").WithVersion("1.0.0").WithOS("linux").WithArch("amd64").WithFmt("raw"))
	assert.Equal(t, "https://example.com/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", u)
}

func TestURL_WithDebug(t *testing.T) {
	u := URL("https://example.com", For("myapp").WithVersion("1").WithOS("linux").WithArch("amd64").WithFmt("raw").WithDebug(true))
	assert.Equal(t, "https://example.com/static?arch=amd64&debug=1&fmt=raw&id=myapp&os=linux&v=1", u)
}

func TestURL_ParamsSorted(t *testing.T) {
	u := URL("", For("z-project").WithVersion("9").WithOS("darwin").WithArch("arm64").WithFmt("npm"))
	assert.Equal(t, "/static?arch=arm64&fmt=npm&id=z-project&os=darwin&v=9", u)
}

func TestServe_MissingVersion(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?arch=amd64&id=myapp&os=linux", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_LatestVersion(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?arch=amd64&id=myapp&os=linux&v=latest", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_MissingOSArch(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?id=myapp&v=1.0.0", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_UnsupportedFormat(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=nonexistent&id=myapp&os=linux&v=1.0.0", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_CanonicalRedirect(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?v=1&id=myapp&os=linux&arch=amd64&fmt=raw", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Contains(t, loc, "arch=amd64&fmt=raw&id=myapp&os=linux&v=1")
}

func TestServe_StripsUnknownParams(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&garbage=yes&id=myapp&os=linux&v=1", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	loc := rec.Header().Get("Location")
	assert.NotContains(t, loc, "garbage")
}


func TestFmtRegistry(t *testing.T) {
	_, ok := LookupFmt("raw")
	assert.True(t, ok)

	_, ok = LookupFmt("symbols")
	assert.True(t, ok)

	_, ok = LookupFmt("nonexistent")
	assert.False(t, ok)
}

func TestComputeETag(t *testing.T) {
	ctx1 := ServeContext{}
	ctx1.Project.Name = "myapp"
	ctx1.Release.Version = "1.0.0"

	etag1 := computeETag(ctx1, "raw")
	etag2 := computeETag(ctx1, "npm")
	assert.NotEqual(t, etag1, etag2)

	ctx2 := ctx1
	ctx2.Artifact.StorageKey = "abc123"
	etag3 := computeETag(ctx2, "raw")
	assert.NotEqual(t, etag1, etag3)
}

func setupIntegration(t *testing.T) (*staticHandler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	return &staticHandler{DB: d, Store: store, BaseURL: "http://localhost:8080"}, d, store
}

func withProject(r *http.Request, p *db.Project) *http.Request {
	ctx := auth.WithProject(r.Context(), p)
	return r.WithContext(ctx)
}

func TestServe_RawFormat_Success(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("hello-binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "hello-binary")
	assert.NotEmpty(t, rec.Header().Get("ETag"))
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
}

func TestServe_DockerArtifact_NotServed(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1", VersionNum: 1}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("oci-manifest-json"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindDocker, StorageKey: key, Size: size, SHA256: key,
	}))

	// A docker image is OCI-only; /static must not serve it as a raw download.
	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=ollama&os=linux&v=1", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_ETag_NotModified(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag)

	req2 := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", nil)
	req2 = withProject(req2, proj)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.Serve(rec2, req2)
	assert.Equal(t, http.StatusNotModified, rec2.Code)
}

func TestServe_VersionNotFound(t *testing.T) {
	h, d, _ := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=9.9.9", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_ArtifactNotFound(t *testing.T) {
	h, d, _ := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_VersionResolution_StripV(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "2.0.0", VersionNum: 2000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=v2.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServe_VersionResolution_StripDotZeroZero(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp"}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "5", VersionNum: 5}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("bin"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=5.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServe_AnyOSArch(t *testing.T) {
	h, d, _ := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	req := httptest.NewRequest("GET", "/static?arch=any&fmt=raw&id=myapp&os=any&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code, "raw format requires an artifact, so os=any should fail")
}

func TestServe_DebugSymbolsHeader(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("not-an-elf"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	hdr := rec.Header().Get("X-Debug-Symbols")
	assert.Contains(t, []string{"available", "unavailable"}, hdr, "should indicate symbol availability")
}

func TestRedirect(t *testing.T) {
	req := httptest.NewRequest("GET", "/dl/myapp/1.0.0/linux/amd64", nil)
	rec := httptest.NewRecorder()
	Redirect(rec, req, "https://example.com", For("myapp").WithVersion("1.0.0").WithOS("linux").WithArch("amd64").WithFmt("raw"))
	assert.Equal(t, http.StatusFound, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Equal(t, "https://example.com/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=1.0.0", loc)
}

func TestParseRoute_ExtractsID(t *testing.T) {
	req := httptest.NewRequest("GET", "/static?id=myapp&v=1", nil)
	ri := parseRoute(req)
	assert.Equal(t, "myapp", ri.ProjectName())

	req2 := httptest.NewRequest("GET", "/static?id=@buildhost/myapp&v=1", nil)
	ri2 := parseRoute(req2)
	assert.Equal(t, "myapp", ri2.ProjectName(), "strips @buildhost/ prefix")
}

func TestRoute_Access(t *testing.T) {
	r := route{project: "myapp"}
	assert.Equal(t, auth.ReadAccess, r.Access())
}

func TestServe_SymbolsFormat_NoStrip(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("not-elf"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=symbols&id=myapp&os=linux&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_RepackageFormat(t *testing.T) {
	h, d, store := setupIntegration(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	key, size, err := store.Put(ctx, strings.NewReader("binary-data"))
	require.NoError(t, err)
	require.NoError(t, d.CreateArtifact(ctx, &db.Artifact{
		ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64,
		Kind: db.KindBinary, StorageKey: key, Size: size, SHA256: key,
	}))

	RegisterRepackageFmt("tar.gz")

	req := httptest.NewRequest("GET", "/static?arch=amd64&fmt=tar.gz&id=myapp&os=linux&v=1.0.0", nil)
	req = withProject(req, proj)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Body.Bytes())
}

func TestResolveVersion(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer d.Close()

	ctx := context.Background()
	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "3.0.0", VersionNum: 3000000}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	r, err := resolveVersion(ctx, d, proj.ID, "3.0.0")
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", r.Version)

	r, err = resolveVersion(ctx, d, proj.ID, "v3.0.0")
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", r.Version)

	_, err = resolveVersion(ctx, d, proj.ID, "9.9.9")
	assert.ErrorIs(t, err, db.ErrNotFound)

	_, err = resolveVersion(ctx, d, proj.ID, "latest")
	assert.ErrorIs(t, err, db.ErrNotFound, "latest is not a valid version for /static")
}

func TestResolveVersion_AutoVersioning(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	defer d.Close()

	ctx := context.Background()
	proj := &db.Project{Name: "tool"}
	require.NoError(t, d.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: fmt.Sprintf("%d", 7), VersionNum: 7}
	require.NoError(t, d.CreateRelease(ctx, rel))
	require.NoError(t, d.PublishRelease(ctx, rel.ID))

	r, err := resolveVersion(ctx, d, proj.ID, "7")
	require.NoError(t, err)
	assert.Equal(t, "7", r.Version)

	r, err = resolveVersion(ctx, d, proj.ID, "7.0.0")
	require.NoError(t, err)
	assert.Equal(t, "7", r.Version)
}
