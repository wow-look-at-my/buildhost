package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// sitesHost is the Host header used by integration tests that go through the
// real router. The sites service is registered as a subdomain route on
// sites.{domain}, so requests must carry this host.
const sitesHost = "sites.test.local"

// testEnv wires the real auth stack: requests are dispatched through the
// router (host + path matching) and the auth middleware, exactly as in
// production. Used by TestRouting to catch routing-level bugs that unit tests
// using direct handler calls cannot detect.
type testEnv struct {
	handler http.Handler
	db      *db.DB
	store   *storage.Filesystem
	token   string
}

func setupEnv(t *testing.T) *testEnv {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	auth.Init(d, store, t.TempDir(), nil, nil, nil, nil, "", "", "", nil)

	plaintext, _, err := d.CreateToken(context.Background(), "test", nil, "read,write")
	require.NoError(t, err)

	h := auth.GetMiddleware().Authenticate(http.HandlerFunc(auth.ServeHTTP))
	return &testEnv{handler: h, db: d, store: store, token: plaintext}
}

func (e *testEnv) do(t *testing.T, method, path, contentType string, body []byte, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	return e.doHost(t, sitesHost, method, path, contentType, body, authed)
}

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

func (e *testEnv) uploadSite(t *testing.T, project, branch string, files map[string]string) {
	t.Helper()
	rec := e.do(t, "PUT", "/"+project+"/branch/"+branch, "application/gzip", makeTarGz(t, files), true)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

// TestRouting pins the real routing the rest of the suite relies on. It is the
// regression test for the redirect loop fixed by folding the branch-root
// redirect into Serve: a single GET route means a file request reaches Serve
// directly instead of being shadowed by a greedier redirect route.
// This test MUST use setupEnv (real router) -- direct handler calls cannot
// catch routing-level bugs.
func TestRouting(t *testing.T) {
	env := setupEnv(t)
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

// TestServe_NestedDirServesIndexNotDirEntry reproduces the bug where a nested
// directory URL (e.g. /scratchpads/foo/) served the 0-byte tar directory entry
// instead of foo/index.html: the {path...} router value drops the trailing
// slash, so Serve must detect the directory from the request URL, and must
// never serve a directory entry as a file.
func TestServe_NestedDirServesIndexNotDirEntry(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	// A tar that, like GNU tar, carries an explicit directory entry before the
	// nested file. The bug matched that 0-byte "sub/" entry.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0755}))
	body := "<h1>nested</h1>"
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sub/index.html", Typeflag: tar.TypeReg, Size: int64(len(body)), Mode: 0644}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	put := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader(buf.Bytes()))
	put = withRoute(put, proj, route{project: "mysite", branch: "main", write: true})
	prec := httptest.NewRecorder()
	h.Upload(prec, put)
	require.Equal(t, http.StatusCreated, prec.Code)

	// Request the directory: the URL ends with "/" but the router strips it from
	// {path...}, so rt.path is "sub". Serve must fall back to index.html.
	get := httptest.NewRequest("GET", "/sites/mysite/branch/main/sub/", nil)
	get = withRoute(get, proj, route{project: "mysite", branch: "main", path: "sub"})
	grec := httptest.NewRecorder()
	h.Serve(grec, get)

	assert.Equal(t, http.StatusOK, grec.Code)
	assert.Equal(t, body, grec.Body.String())
	assert.Contains(t, grec.Header().Get("Content-Type"), "html")
}

func TestServe_NotFound_Custom404(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	body := "<h1>missing</h1>"
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
		"404.html":   body,
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/missing.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "missing.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, body, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "html")
	assert.Equal(t, "16", rec.Header().Get("Content-Length"))
}

func TestServe_DirectCustom404File(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	body := "<h1>missing</h1>"
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
		"404.html":   body,
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/404.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "404.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, body, rec.Body.String())
}
