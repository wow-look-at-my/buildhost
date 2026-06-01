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
		"dl":     {Scheme: "https", Host: "dl.example.com"},
		"static": {Scheme: "https", Host: "cdn.example.com", Path: "/files"},
	}

	tests := []struct {
		name      string
		subdomain string
		pattern   string
		wantHost  string
	}{
		{"simple", "apt", "/", "apt.example.com"},
		{"with method", "dl", "GET /{project}", "dl.example.com"},
		{"with path prefix", "static", "GET /file", "cdn.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServiceRoute(tt.subdomain, tt.pattern)
			assert.Contains(t, got, tt.wantHost)
		})
	}
}

func TestServiceRedirect(t *testing.T) {
	serviceURLs = map[string]*url.URL{
		"docker": {Scheme: "https", Host: "docker.example.com"},
		"oci":    {Scheme: "https", Host: "oci.example.com"},
	}

	ServiceRedirect("docker", "oci", true)

	ts := httptest.NewServer(http.HandlerFunc(ServeHTTP))
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", ts.URL+"/v2/myapp/manifests/latest", nil)
	req.Host = "docker.example.com"
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "https://oci.example.com/v2/myapp/manifests/latest", resp.Header.Get("Location"))
}

func TestServiceRedirect_Temporary(t *testing.T) {
	serviceURLs = map[string]*url.URL{
		"old": {Scheme: "https", Host: "old.example.com"},
		"new": {Scheme: "https", Host: "new.example.com"},
	}

	ServiceRedirect("old", "new", false)

	ts := httptest.NewServer(http.HandlerFunc(ServeHTTP))
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", ts.URL+"/path?q=1", nil)
	req.Host = "old.example.com"
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "https://new.example.com/path?q=1", resp.Header.Get("Location"))
}

func TestServiceRedirect_NilURLs(t *testing.T) {
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
