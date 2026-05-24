package static

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
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

func TestResolveVersion(t *testing.T) {
	// resolveVersion is tested implicitly through the handler, but
	// we can test the fallback logic directly once we have a DB.
	// For now, test that it returns ErrNotFound for nonexistent versions.
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
