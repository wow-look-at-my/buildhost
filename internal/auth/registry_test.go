package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceRedirect(t *testing.T) {
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

func TestServeHTTP_SubdomainDispatch(t *testing.T) {
	ServiceHandleRaw("mytest", "GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("from mytest service"))
	})

	ts := httptest.NewServer(http.HandlerFunc(ServeHTTP))
	defer ts.Close()

	client := &http.Client{}
	req, _ := http.NewRequest("GET", ts.URL+"/hello", nil)
	req.Host = "mytest.pazer.build"
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServeHTTP_FallsThrough(t *testing.T) {
	HandleRaw("GET /api-test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(http.HandlerFunc(ServeHTTP))
	defer ts.Close()

	client := &http.Client{}
	req, _ := http.NewRequest("GET", ts.URL+"/api-test", nil)
	req.Host = "unknown.whatever.com"
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDeriveServiceURL(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "dl.pazer.build"

	u := DeriveServiceURL(req, "static")
	assert.Equal(t, "https", u.Scheme)
	assert.Equal(t, "static.pazer.build", u.Host)
}

func TestDeriveServiceURL_HTTP(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "dl.localhost:8080"

	u := DeriveServiceURL(req, "static")
	assert.Equal(t, "http", u.Scheme)
	assert.Equal(t, "static.localhost", u.Host)
}

func TestRequestScheme(t *testing.T) {
	cases := []struct {
		host	string
		want	string
	}{
		{"localhost", "http"},
		{"localhost:8080", "http"},
		{"dl.localhost:8080", "http"},
		{"127.0.0.1", "http"},
		{"127.0.0.1:8080", "http"},
		{"pazer.build", "https"},
		{"dl.pazer.build", "https"},
		{"buildhost.example.com:8443", "https"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tc.host
		assert.Equalf(t, tc.want, RequestScheme(req), "host=%q", tc.host)
	}
}

func TestRequestBaseURL_PreservesHostAndPort(t *testing.T) {
	cases := []struct {
		host	string
		want	string
	}{
		// Production: no port on the Host header -> clean https URL.
		{"pazer.build", "https://pazer.build"},
		{"dl.pazer.build", "https://dl.pazer.build"},
		// Local/dev and direct access: the port is part of how the client
		// reached us and must survive into self-referential links.
		{"localhost:8080", "http://localhost:8080"},
		{"127.0.0.1:54321", "http://127.0.0.1:54321"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tc.host
		assert.Equalf(t, tc.want, RequestBaseURL(req), "host=%q", tc.host)
	}
}
