package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestServiceRoute(t *testing.T) {
	serviceURLs = map[string]*url.URL{
		"apt":    {Scheme: "https", Host: "apt.example.com"},
		"dl":    {Scheme: "https", Host: "dl.example.com"},
		"static": {Scheme: "https", Host: "cdn.example.com", Path: "/files"},
	}

	tests := []struct {
		name      string
		subdomain string
		pattern   string
		want      string
	}{
		{"simple host", "apt", "/", "apt.example.com/"},
		{"with method", "dl", "GET /{project}", "GET dl.example.com/{project}"},
		{"with path prefix", "static", "GET /file", "GET cdn.example.com/files/file"},
		{"catch-all", "apt", "/{path...}", "apt.example.com/{path...}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceRoute(tt.subdomain, tt.pattern)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServiceRedirect(t *testing.T) {
	oldMux := mux
	mux = http.NewServeMux()
	t.Cleanup(func() { mux = oldMux })

	serviceURLs = map[string]*url.URL{
		"docker": {Scheme: "https", Host: "docker.example.com"},
		"oci":    {Scheme: "https", Host: "oci.example.com"},
	}

	ServiceRedirect("docker", "oci", true)

	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req.Host = "docker.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	loc := rec.Header().Get("Location")
	assert.Equal(t, "https://oci.example.com/v2/myapp/manifests/latest", loc)
}

func TestServiceRedirect_Temporary(t *testing.T) {
	oldMux := mux
	mux = http.NewServeMux()
	t.Cleanup(func() { mux = oldMux })

	serviceURLs = map[string]*url.URL{
		"old": {Scheme: "https", Host: "old.example.com"},
		"new": {Scheme: "https", Host: "new.example.com"},
	}

	ServiceRedirect("old", "new", false)

	req := httptest.NewRequest("GET", "/path?q=1", nil)
	req.Host = "old.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "https://new.example.com/path?q=1", rec.Header().Get("Location"))
}

func TestServiceRedirect_NilURLs(t *testing.T) {
	oldMux := mux
	mux = http.NewServeMux()
	t.Cleanup(func() { mux = oldMux })

	serviceURLs = map[string]*url.URL{}
	ServiceRedirect("missing", "also-missing", true)
}

func TestStaticURL(t *testing.T) {
	serviceURLs = map[string]*url.URL{
		"static": {Scheme: "https", Host: "static.example.com"},
	}
	got := StaticURL()
	require.NotNil(t, got)
	assert.Equal(t, "static.example.com", got.Host)
}

func TestDLBaseURL(t *testing.T) {
	serviceURLs = map[string]*url.URL{
		"dl": {Scheme: "https", Host: "dl.example.com"},
	}
	got := DLBaseURL()
	require.NotNil(t, got)
	assert.Equal(t, "dl.example.com", got.Host)
}

func TestServiceURL(t *testing.T) {
	serviceURLs = map[string]*url.URL{
		"apt": {Scheme: "https", Host: "apt.example.com"},
	}
	assert.NotNil(t, ServiceURL("apt"))
	assert.Nil(t, ServiceURL("nonexistent"))
}
