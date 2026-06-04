package sites

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// The sites service is registered as a subdomain route (sites.{domain}/...), so
// every request in these tests carries this Host. The handler is NOT reachable
// at "{apex}/sites/..." -- see TestRouting, which pins that.
const sitesHost = "sites.test.local"

// testEnv wires the real auth stack: requests are dispatched through the router
// (host + path matching) and the auth middleware, exactly as in production.
// Tests must NOT call handler methods directly -- doing so bypasses the routing
// and auth that this package is responsible for, and is how a redirect-loop bug
// that made every served file unreachable went unnoticed (see TestRouting).
type testEnv struct {
	handler http.Handler
	db      *db.DB
	store   *storage.Filesystem
	token   string // global API token with read,write scope
}

func setupTest(t *testing.T, fetchDomains ...string) *testEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	// Init wires the shared singletons and runs the OnReady callbacks, binding
	// the package-global sites handler to this DB/store/fetch-domains. The sites
	// package's init() has already registered its routes on the shared router.
	auth.Init(d, store, t.TempDir(), nil, nil, nil, fetchDomains)

	plaintext, _, err := d.CreateToken(context.Background(), "test", nil, "read,write")
	require.NoError(t, err)

	h := auth.GetMiddleware().Authenticate(http.HandlerFunc(auth.ServeHTTP))
	return &testEnv{handler: h, db: d, store: store, token: plaintext}
}

func seedProject(t *testing.T, d *db.DB, name string) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

// do dispatches a request to the sites service through the real router. path is
// the service-relative path the route is registered under (e.g.
// "/mysite/branch/main"), NOT a "/sites/..." apex path.
func (e *testEnv) do(t *testing.T, method, path, contentType string, body []byte, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	return e.doHost(t, sitesHost, method, path, contentType, body, authed)
}

// doHost is do with an explicit Host, for asserting which host the service
// answers on.
func (e *testEnv) doHost(t *testing.T, host, method, path, contentType string, body []byte, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     name,
			Size:     int64(len(content)),
			Mode:     0644,
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// uploadSite deploys a tar.gz site through the router and asserts success.
func (e *testEnv) uploadSite(t *testing.T, project, branch string, files map[string]string) {
	t.Helper()
	rec := e.do(t, "PUT", "/"+project+"/branch/"+branch, "application/gzip", makeTarGz(t, files), true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestRouting pins the real routing the rest of the suite relies on, and is the
// regression test for the redirect loop fixed by folding the branch-root
// redirect into Serve: a single GET route means a file request reaches Serve
// directly instead of being shadowed by a greedier redirect route.
func TestRouting(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{
		"index.html": "<h1>hi</h1>",
		"style.css":  "body{}",
	})

	// A file under a branch reaches Serve and is served -- not 301-redirected
	// into a loop (the bug). This only passes via the real router.
	rec := env.do(t, "GET", "/mysite/branch/main/style.css", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())

	// The service answers on the sites subdomain, not on the apex with a
	// "/sites/..." path prefix. An apex request never reaches the handler.
	apex := env.doHost(t, "test.local", "GET", "/sites/mysite/branch/main/style.css", "", nil, false)
	assert.Equal(t, http.StatusNotFound, apex.Code, "apex /sites path must not reach the sites handler")
}

func TestUpload(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	body := makeTarGz(t, map[string]string{"index.html": "<h1>hello</h1>"})
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/gzip", body, true)

	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var site db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&site))
	assert.Equal(t, "main", site.Branch)
	assert.Equal(t, int64(1), site.FileCount)
}

func TestUpload_Unauthorized(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	body := makeTarGz(t, map[string]string{"index.html": "hi"})
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/gzip", body, false)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUpload_InvalidGzip(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	rec := env.do(t, "PUT", "/mysite/branch/main", "application/gzip", []byte("not gzip"), true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpload_EmptyArchive(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.Close()
	gw.Close()

	rec := env.do(t, "PUT", "/mysite/branch/main", "application/gzip", buf.Bytes(), true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_File(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{
		"index.html": "<h1>hello</h1>",
		"style.css":  "body{}",
	})

	rec := env.do(t, "GET", "/mysite/branch/main/style.css", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "css")
}

// A served site asset must carry a CSP that lets the site load its own
// resources. The server-wide middleware sets "default-src 'none'" (right for the
// JSON/binary API); Serve overrides it so hosted pages are not blanked by it.
func TestServe_CSPAllowsOwnAssets(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{
		"index.html": "<script src=app.js></script>",
		"app.js":     "console.log(1)",
	})

	for _, path := range []string{"/mysite/branch/main/", "/mysite/branch/main/app.js"} {
		rec := env.do(t, "GET", path, "", nil, false)
		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Equal(t, "default-src 'self' data:", rec.Header().Get("Content-Security-Policy"), path)
	}
}

func TestServe_IndexFallback(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "<h1>hello</h1>"})

	rec := env.do(t, "GET", "/mysite/branch/main/", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>hello</h1>", rec.Body.String())
}

func TestServe_NotFound_NoBranch(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	rec := env.do(t, "GET", "/mysite/branch/main/foo.html", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_NotFound_NoFile(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "<h1>hello</h1>"})

	rec := env.do(t, "GET", "/mysite/branch/main/missing.html", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestServe_Redirect: a branch root requested without a trailing slash redirects
// to the slashed form (so index.html's relative links resolve under the branch).
// Serve -- the single GET route -- handles this; there is no separate redirect
// route that could shadow file serving.
func TestServe_Redirect(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "<h1>hello</h1>"})

	rec := env.do(t, "GET", "/mysite/branch/main", "", nil, false)
	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/mysite/branch/main/", rec.Header().Get("Location"))
}

func TestDelete(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "<h1>hello</h1>"})

	rec := env.do(t, "DELETE", "/mysite/branch/main", "", nil, true)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec2 := env.do(t, "GET", "/mysite/branch/main/index.html", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec2.Code)
}

func TestDelete_Unauthorized(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "hi"})

	rec := env.do(t, "DELETE", "/mysite/branch/main", "", nil, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDelete_NotFound(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	rec := env.do(t, "DELETE", "/mysite/branch/main", "", nil, true)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestList(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "main"})
	env.uploadSite(t, "mysite", "dev", map[string]string{"index.html": "dev"})

	rec := env.do(t, "GET", "/mysite/branches", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)

	var sites []db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&sites))
	assert.Len(t, sites, 2)
}

func TestList_Empty(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	rec := env.do(t, "GET", "/mysite/branches", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]\n", rec.Body.String())
}

func TestServe_SubdirIndex(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{
		"index.html":      "<h1>root</h1>",
		"docs/index.html": "<h1>docs</h1>",
	})

	rec := env.do(t, "GET", "/mysite/branch/main/docs/", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>docs</h1>", rec.Body.String())
}

func TestUpload_GitCommitHeader(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	body := makeTarGz(t, map[string]string{"index.html": "hi"})
	req := httptest.NewRequest("PUT", "/mysite/branch/main", bytes.NewReader(body))
	req.Host = sitesHost
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set("X-Git-Commit", "abc123")
	req.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var site db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&site))
	assert.Equal(t, "abc123", site.GitCommit)
}

func TestUpload_ReplacesExisting(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "v1"})
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": "v2"})

	rec := env.do(t, "GET", "/mysite/branch/main/", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "v2", rec.Body.String())
}

func TestServe_ContentLength(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")
	content := "<h1>hello world</h1>"
	env.uploadSite(t, "mysite", "main", map[string]string{"index.html": content})

	rec := env.do(t, "GET", "/mysite/branch/main/index.html", "", nil, false)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fmt.Sprintf("%d", len(content)), rec.Header().Get("Content-Length"))
}

func TestUpload_Zip(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	body := makeZip(t, map[string]string{
		"index.html": "<h1>hello</h1>",
		"style.css":  "body{}",
	})
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/zip", body, true)
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var site db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&site))
	assert.Equal(t, "main", site.Branch)
	assert.Equal(t, int64(2), site.FileCount)
}

func TestServe_ZipUpload(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	body := makeZip(t, map[string]string{"index.html": "<h1>from zip</h1>"})
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/zip", body, true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec2 := env.do(t, "GET", "/mysite/branch/main/", "", nil, false)
	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "<h1>from zip</h1>", rec2.Body.String())
}

func TestUpload_InvalidZip(t *testing.T) {
	env := setupTest(t)
	seedProject(t, env.db, "mysite")

	rec := env.do(t, "PUT", "/mysite/branch/main", "application/zip", []byte("not a zip"), true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpload_Fetch(t *testing.T) {
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

	remoteIP := remote.Listener.Addr().(*net.TCPAddr).IP.String()
	env := setupTest(t, remoteIP)
	seedProject(t, env.db, "mysite")

	body := []byte(fmt.Sprintf(`{"url":%q,"headers":{"Authorization":"Bearer test-token"}}`, remote.URL+"/artifact.zip"))
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/json", body, true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// Verify the site is served correctly.
	rec2 := env.do(t, "GET", "/mysite/branch/main/", "", nil, false)
	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "<h1>fetched</h1>", rec2.Body.String())
}

func TestUpload_Fetch_DomainNotAllowed(t *testing.T) {
	env := setupTest(t, "allowed.example.com")
	seedProject(t, env.db, "mysite")

	body := []byte(`{"url":"https://evil.example.com/site.zip"}`)
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/json", body, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not in allowed list")
}

func TestUpload_Fetch_Disabled(t *testing.T) {
	env := setupTest(t) // no fetch domains -> fetch mode disabled
	seedProject(t, env.db, "mysite")

	body := []byte(`{"url":"https://example.com/site.zip"}`)
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/json", body, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not enabled")
}

func TestUpload_Fetch_InvalidJSON(t *testing.T) {
	env := setupTest(t, "example.com")
	seedProject(t, env.db, "mysite")

	rec := env.do(t, "PUT", "/mysite/branch/main", "application/json", []byte("not json"), true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpload_Fetch_HttpURL(t *testing.T) {
	env := setupTest(t, "example.com")
	seedProject(t, env.db, "mysite")

	body := []byte(`{"url":"http://example.com/site.zip"}`)
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/json", body, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "only https")
}

func TestUpload_Fetch_NonOK(t *testing.T) {
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer remote.Close()

	orig := siteFetchClient
	siteFetchClient = remote.Client()
	defer func() { siteFetchClient = orig }()

	remoteIP := remote.Listener.Addr().(*net.TCPAddr).IP.String()
	env := setupTest(t, remoteIP)
	seedProject(t, env.db, "mysite")

	body := []byte(fmt.Sprintf(`{"url":%q}`, remote.URL+"/artifact.zip"))
	rec := env.do(t, "PUT", "/mysite/branch/main", "application/json", body, true)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "fetch returned 404")
}

// --- pure-function unit tests (no routing involved) ------------------------

func TestValidateTar_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "../etc/passwd", Size: 4, Mode: 0644, Typeflag: tar.TypeReg,
	}))
	tw.Write([]byte("evil"))
	tw.Close()

	_, err := validateTar(&buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestValidateTar_AbsolutePath(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "/etc/passwd", Size: 4, Mode: 0644, Typeflag: tar.TypeReg,
	}))
	tw.Write([]byte("evil"))
	tw.Close()

	_, err := validateTar(&buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestValidateTar_Symlink(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd",
	}))
	tw.Close()

	_, err := validateTar(&buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported entry type")
}

func TestContentType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"index.html", "text/html"},
		{"style.css", "text/css"},
		{"app.js", "javascript"},
		{"font.woff2", "font/woff2"},
		{"font.woff", "font/woff"},
		{"app.mjs", "javascript"},
		{"data.bin", "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contentType(tt.name)
			assert.Contains(t, got, tt.want)
		})
	}
}

func TestRouteAccess(t *testing.T) {
	r := route{project: "p", branch: "b", write: true}
	assert.Equal(t, auth.WriteAccess, r.Access())

	r.write = false
	assert.Equal(t, auth.ReadAccess, r.Access())
}

func TestParseRoute(t *testing.T) {
	req := httptest.NewRequest("PUT", "/myapp/branch/main/some/file.txt", nil)
	req.SetPathValue("project", "myapp")
	req.SetPathValue("branch", "main")
	req.SetPathValue("path", "some/file.txt")

	ri := parseRoute(req)
	r := ri.(route)
	assert.Equal(t, "myapp", r.ProjectName())
	assert.Equal(t, "main", r.branch)
	assert.Equal(t, "some/file.txt", r.path)
	assert.True(t, r.write)

	req2 := httptest.NewRequest("GET", "/myapp/branch/dev/index.html", nil)
	req2.SetPathValue("project", "myapp")
	req2.SetPathValue("branch", "dev")
	req2.SetPathValue("path", "index.html")

	r2 := parseRoute(req2).(route)
	assert.False(t, r2.write)
}

func TestParseRoute_BranchList(t *testing.T) {
	req := httptest.NewRequest("GET", "/myapp/branches", nil)
	req.SetPathValue("project", "myapp")

	ri := parseRoute(req)
	r := ri.(route)
	assert.Equal(t, "myapp", r.ProjectName())
	assert.Equal(t, "", r.branch)
}

func TestZipToTar_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../etc/passwd")
	require.NoError(t, err)
	w.Write([]byte("evil"))
	zw.Close()

	var out bytes.Buffer
	_, err = zipToTar(buf.Bytes(), &out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestZipToTar_AbsolutePath(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("/etc/passwd")
	require.NoError(t, err)
	w.Write([]byte("evil"))
	zw.Close()

	var out bytes.Buffer
	_, err = zipToTar(buf.Bytes(), &out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}
