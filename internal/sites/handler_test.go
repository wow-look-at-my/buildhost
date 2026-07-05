package sites

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func withRoute(r *http.Request, project *db.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

func setupTest(t *testing.T) (*Handler, *db.DB, *storage.Filesystem) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	h := &Handler{DB: d, Store: store}
	return h, d, store
}

func seedProject(t *testing.T, d *db.DB, name string) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
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

func uploadSite(t *testing.T, h *Handler, proj *db.Project, branch string, files map[string]string) {
	t.Helper()
	body := makeTarGz(t, files)
	req := httptest.NewRequest("PUT", "/sites/"+proj.Name+"/branch/"+branch, bytes.NewReader(body))
	req = withRoute(req, proj, route{project: proj.Name, branch: branch, write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestUpload_PublicSiteFlag(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "priv")

	// X-Public-Site: true marks the site public.
	body := makeTarGz(t, map[string]string{"index.html": "<h1>hi</h1>"})
	req := httptest.NewRequest("PUT", "/sites/priv/branch/pr-1", bytes.NewReader(body))
	req.Header.Set("X-Public-Site", "true")
	req = withRoute(req, proj, route{project: "priv", branch: "pr-1", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	site, err := d.GetSite(context.Background(), proj.ID, "pr-1")
	require.NoError(t, err)
	assert.True(t, site.IsPublic, "X-Public-Site: true should persist as public")

	// The Serve route reports this branch as publicly readable; write and the
	// branch listing never do.
	assert.True(t, route{project: "priv", branch: "pr-1"}.AllowsPublicRead(context.Background(), d, proj))
	assert.False(t, route{project: "priv", branch: "pr-1", write: true}.AllowsPublicRead(context.Background(), d, proj))
	assert.False(t, route{project: "priv", branch: ""}.AllowsPublicRead(context.Background(), d, proj))

	// Without the header a site stays private (gated).
	uploadSite(t, h, proj, "pr-2", map[string]string{"index.html": "x"})
	gated, err := d.GetSite(context.Background(), proj.ID, "pr-2")
	require.NoError(t, err)
	assert.False(t, gated.IsPublic)
	assert.False(t, route{project: "priv", branch: "pr-2"}.AllowsPublicRead(context.Background(), d, proj))
}

func TestUpload(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	body := makeTarGz(t, map[string]string{
		"index.html": "<h1>hello</h1>",
	})

	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader(body))
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var site db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&site))
	assert.Equal(t, "main", site.Branch)
	assert.Equal(t, int64(1), site.FileCount)
}

func TestUpload_InvalidGzip(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader([]byte("not gzip")))
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpload_EmptyArchive(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	tw.Close()
	gw.Close()

	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader(buf.Bytes()))
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServe_File(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
		"style.css":  "body{}",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/style.css", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "style.css"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "css")
}

func TestServe_SetsSiteSecurityHeaders(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html":     "<h1>hi</h1>",
		"assets/app.mjs": "export default 1;",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/assets/app.mjs", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "assets/app.mjs"})
	rec := httptest.NewRecorder()
	// The global security middleware sets these strict app headers before the
	// handler runs; serving a site must drop them so its assets can load.
	rec.Header().Set("Content-Security-Policy", "default-src 'none'")
	rec.Header().Set("X-Frame-Options", "DENY")
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Security-Policy"))
	assert.Empty(t, rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	assert.Equal(t, "credentialless", rec.Header().Get("Cross-Origin-Embedder-Policy"))
}

func TestServe_IndexFallback(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: ""})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>hello</h1>", rec.Body.String())
}

func TestServe_NotFound_NoBranch(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/foo.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "foo.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_NotFound_NoFile(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/missing.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "missing.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeRedirect(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "<h1>hello</h1>"})

	// A branch root requested without a trailing slash redirects to the slashed
	// form (so index.html's relative links resolve under the branch). Serve --
	// the single GET route -- handles this; there is no separate redirect route
	// that could shadow file serving.
	req := httptest.NewRequest("GET", "/sites/mysite/branch/main", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: ""})
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Security-Policy", "default-src 'none'")
	rec.Header().Set("X-Frame-Options", "DENY")
	h.Serve(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/sites/mysite/branch/main/", rec.Header().Get("Location"))
	assert.Empty(t, rec.Header().Get("Content-Security-Policy"))
	assert.Empty(t, rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	assert.Equal(t, "credentialless", rec.Header().Get("Cross-Origin-Embedder-Policy"))
}

func TestDelete(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
	})

	req := httptest.NewRequest("DELETE", "/sites/mysite/branch/main", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)

	req2 := httptest.NewRequest("GET", "/sites/mysite/branch/main/index.html", nil)
	req2 = withRoute(req2, proj, route{project: "mysite", branch: "main", path: "index.html"})
	rec2 := httptest.NewRecorder()
	h.Serve(rec2, req2)

	assert.Equal(t, http.StatusNotFound, rec2.Code)
}

func TestDelete_NotFound(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	req := httptest.NewRequest("DELETE", "/sites/mysite/branch/main", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestList(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "main"})
	uploadSite(t, h, proj, "dev", map[string]string{"index.html": "dev"})

	req := httptest.NewRequest("GET", "/api/v1/projects/mysite/sites", nil)
	req = withRoute(req, proj, route{project: "mysite"})
	rec := httptest.NewRecorder()
	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var sites []db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&sites))
	assert.Len(t, sites, 2)
}

func TestList_Empty(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	req := httptest.NewRequest("GET", "/api/v1/projects/mysite/sites", nil)
	req = withRoute(req, proj, route{project: "mysite"})
	rec := httptest.NewRecorder()
	h.List(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Equal(t, "[]\n", body)
}

func TestServe_SubdirIndex(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html":      "<h1>root</h1>",
		"docs/index.html": "<h1>docs</h1>",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/docs/", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "docs/"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>docs</h1>", rec.Body.String())
}

func TestUpload_GitCommitHeader(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	body := makeTarGz(t, map[string]string{"index.html": "hi"})
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader(body))
	req.Header.Set("X-Git-Commit", "abc123")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var site db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&site))
	assert.Equal(t, "abc123", site.GitCommit)
}

func TestUpload_ReplacesExisting(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "v1"})

	uploadSite(t, h, proj, "main", map[string]string{"index.html": "v2"})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: ""})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "v2", rec.Body.String())
}

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

func TestServe_ContentLength(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	content := "<h1>hello world</h1>"
	uploadSite(t, h, proj, "main", map[string]string{"index.html": content})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/index.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "index.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fmt.Sprintf("%d", len(content)), rec.Header().Get("Content-Length"))
}

func TestRouteAccess(t *testing.T) {
	r := route{project: "p", branch: "b", write: true}
	assert.Equal(t, auth.WriteAccess, r.Access())

	r.write = false
	assert.Equal(t, auth.ReadAccess, r.Access())
}

func TestParseRoute(t *testing.T) {
	req := httptest.NewRequest("PUT", "/sites/myapp/branch/main/some/file.txt", nil)
	req.SetPathValue("project", "myapp")
	req.SetPathValue("branch", "main")
	req.SetPathValue("path", "some/file.txt")

	ri := parseRoute(req)
	r := ri.(route)
	assert.Equal(t, "myapp", r.ProjectName())
	assert.Equal(t, "main", r.branch)
	assert.Equal(t, "some/file.txt", r.path)
	assert.True(t, r.write)

	req2 := httptest.NewRequest("GET", "/sites/myapp/branch/dev/index.html", nil)
	req2.SetPathValue("project", "myapp")
	req2.SetPathValue("branch", "dev")
	req2.SetPathValue("path", "index.html")

	r2 := parseRoute(req2).(route)
	assert.False(t, r2.write)
}

func TestParseRoute_BranchList(t *testing.T) {
	req := httptest.NewRequest("GET", "/sites/myapp/branches", nil)
	req.SetPathValue("project", "myapp")

	ri := parseRoute(req)
	r := ri.(route)
	assert.Equal(t, "myapp", r.ProjectName())
	assert.Equal(t, "", r.branch)
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

func TestUpload_Zip(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	body := makeZip(t, map[string]string{
		"index.html": "<h1>hello</h1>",
		"style.css":  "body{}",
	})

	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var site db.Site
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&site))
	assert.Equal(t, "main", site.Branch)
	assert.Equal(t, int64(2), site.FileCount)
}

func TestServe_ZipUpload(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	body := makeZip(t, map[string]string{"index.html": "<h1>from zip</h1>"})
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	req2 := httptest.NewRequest("GET", "/sites/mysite/branch/main/", nil)
	req2 = withRoute(req2, proj, route{project: "mysite", branch: "main", path: ""})
	rec2 := httptest.NewRecorder()
	h.Serve(rec2, req2)

	assert.Equal(t, http.StatusOK, rec2.Code)
	assert.Equal(t, "<h1>from zip</h1>", rec2.Body.String())
}

func TestUpload_InvalidZip(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", bytes.NewReader([]byte("not a zip")))
	req.Header.Set("Content-Type", "application/zip")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestZipToTar_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../etc/passwd")
	require.NoError(t, err)
	w.Write([]byte("evil"))
	zw.Close()

	var out bytes.Buffer
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	_, err = zipToTar(zr, &out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
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

	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	h.FetchDomains = []string{remote.Listener.Addr().(*net.TCPAddr).IP.String()}

	body := fmt.Sprintf(`{"url":%q,"headers":{"Authorization":"Bearer test-token"}}`, remote.URL+"/artifact.zip")
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

	body := fmt.Sprintf(`{"url":%q}`, remote.URL+"/artifact.zip")
	req := httptest.NewRequest("PUT", "/sites/mysite/branch/main", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRoute(req, proj, route{project: "mysite", branch: "main", write: true})
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "fetch returned 404")
}

func TestZipToTar_AbsolutePath(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("/etc/passwd")
	require.NoError(t, err)
	w.Write([]byte("evil"))
	zw.Close()

	var out bytes.Buffer
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	_, err = zipToTar(zr, &out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}
