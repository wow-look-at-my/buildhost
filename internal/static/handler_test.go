package static

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestCanonicalQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already sorted", "arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", "arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0"},
		{"unsorted", "v=1.0.0&project=myapp&os=linux&arch=amd64&fmt=raw", "arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0"},
		{"strips unknown", "arch=amd64&foo=bar&project=myapp&os=linux&v=1", "arch=amd64&os=linux&project=myapp&v=1"},
		{"keeps debug", "debug=1&project=myapp&v=1&os=linux&arch=amd64", "arch=amd64&debug=1&os=linux&project=myapp&v=1"},
		{"normalizes os/arch aliases", "arch=X64&os=Linux&project=myapp&v=1", "arch=amd64&os=linux&project=myapp&v=1"},
		{"keeps any sentinel", "arch=any&fmt=npm-wrapper&os=any&project=myapp&v=1", "arch=any&fmt=npm-wrapper&os=any&project=myapp&v=1"},
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
	base, _ := url.Parse("https://example.com")
	u := URL(base, For("myapp").WithVersion("1.0.0").WithOS("linux").WithArch("amd64").WithFmt("raw"))
	assert.Equal(t, "https://example.com/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", u)
}

func TestURL_WithDebug(t *testing.T) {
	base, _ := url.Parse("https://example.com")
	u := URL(base, For("myapp").WithVersion("1").WithOS("linux").WithArch("amd64").WithFmt("raw").WithDebug(true))
	assert.Equal(t, "https://example.com/file?arch=amd64&debug=1&fmt=raw&os=linux&project=myapp&v=1", u)
}

func TestURL_ParamsSorted(t *testing.T) {
	base, _ := url.Parse("")
	u := URL(base, For("z-project").WithVersion("9").WithOS("darwin").WithArch("arm64").WithFmt("npm"))
	assert.Equal(t, "/file?arch=arm64&fmt=npm&os=darwin&project=z-project&v=9", u)
}

func TestServe_MissingVersion(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?arch=amd64&os=linux&project=myapp", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_LatestVersion(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?arch=amd64&os=linux&project=myapp&v=latest", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_MissingOSArch(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?project=myapp&v=1.0.0", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_UnsupportedFormat(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=nonexistent&os=linux&project=myapp&v=1.0.0", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_CanonicalRedirect(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?v=1&project=myapp&os=linux&arch=amd64&fmt=raw", nil)
	rec := httptest.NewRecorder()
	h := &staticHandler{}
	h.Serve(rec, req)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Contains(t, loc, "arch=amd64&fmt=raw&os=linux&project=myapp&v=1")
}

func TestServe_StripsUnknownParams(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?arch=amd64&fmt=raw&garbage=yes&os=linux&project=myapp&v=1", nil)
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

func TestRedirect(t *testing.T) {
	req := httptest.NewRequest("GET", "/dl/myapp/1.0.0/linux/amd64", nil)
	rec := httptest.NewRecorder()
	base, _ := url.Parse("https://example.com")
	Redirect(rec, req, base, For("myapp").WithVersion("1.0.0").WithOS("linux").WithArch("amd64").WithFmt("raw"), http.StatusFound)
	assert.Equal(t, http.StatusFound, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Equal(t, "https://example.com/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=1.0.0", loc)
}

func TestParseRoute_ExtractsID(t *testing.T) {
	req := httptest.NewRequest("GET", "/file?project=myapp&v=1", nil)
	ri := parseRoute(req)
	assert.Equal(t, "myapp", ri.ProjectName())

	req2 := httptest.NewRequest("GET", "/file?project=other-app&v=1", nil)
	ri2 := parseRoute(req2)
	assert.Equal(t, "other-app", ri2.ProjectName())
}

func TestRoute_Access(t *testing.T) {
	r := route{project: "myapp"}
	assert.Equal(t, auth.ReadAccess, r.Access())
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

func TestAcceptsZstd(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"zstd", true},
		{"gzip, deflate, br, zstd", true},
		{"zstd;q=0.5", true},
		{"ZSTD", true},
		{" zstd ", true},
		{"", false},
		{"gzip, deflate", false},
		{"gzip", false},
		{"zstd;q=0", false},
		{"zstd;q=0.0", false},
		{"*", false}, // a bare "*" must not earn zstd; the client must name it
		{"identity", false},
	}
	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			assert.Equal(t, tt.want, acceptsZstd(tt.accept))
		})
	}
}
