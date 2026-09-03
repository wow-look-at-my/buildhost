package sites

// Fetch-upload tests: deploying a site by pointing buildhost at a remote
// archive URL (domain allowlist, scheme and error handling).

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpload_Fetch(t *testing.T) {
	t.Serial()
	// Serve a zip from an httptest server acting as the remote.
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/zip")
		w.Write(makeZip(t, map[string]string{"index.html": "<h1>fetched</h1>"}))
	}))
	defer remote.Close()

	// Swap in the TLS client from the test server.
	orig := siteFetchClient
	siteFetchClient = remote.Client()
	defer func() { siteFetchClient = orig }()

	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	h.FetchDomains = []string{remote.Listener.Addr().(*net.TCPAddr).IP.String()}

	// Marshalled, not formatted: %q is Go quoting, which is not JSON escaping.
	spec, err := json.Marshal(map[string]any{
		"url":     remote.URL + "/artifact.zip",
		"headers": map[string]string{"Authorization": "Bearer test-token"},
	})
	require.NoError(t, err)
	body := string(spec)
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	// Verify the site is served correctly.
	req2 := httptest.NewRequest("GET", "/sites/mysite/branch/main/", nil)
	req2 = withRoute(req2, proj, route{project: "mysite", branch: "main", path: ""})
	rec2 := httptest.NewRecorder()
	h.Serve(rec2, req2)
	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "<h1>fetched</h1>", rec2.Body.String())
}

func TestUpload_Fetch_DomainNotAllowed(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	h.FetchDomains = []string{"allowed.example.com"}

	body := `{"url":"https://evil.example.com/site.zip"}`
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not in allowed list")
}

func TestUpload_Fetch_Disabled(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	// FetchDomains is empty — fetch mode disabled.

	body := `{"url":"https://example.com/site.zip"}`
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not enabled")
}

func TestUpload_Fetch_InvalidJSON(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	h.FetchDomains = []string{"example.com"}

	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpload_Fetch_HttpURL(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	h.FetchDomains = []string{"example.com"}

	body := `{"url":"http://example.com/site.zip"}`
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "only https")
}

func TestUpload_Fetch_NonOK(t *testing.T) {
	t.Serial()
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer remote.Close()

	orig := siteFetchClient
	siteFetchClient = remote.Client()
	defer func() { siteFetchClient = orig }()

	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	h.FetchDomains = []string{remote.Listener.Addr().(*net.TCPAddr).IP.String()}

	spec, err := json.Marshal(map[string]string{"url": remote.URL + "/artifact.zip"})
	require.NoError(t, err)
	body := string(spec)
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "fetch returned 404")
}
