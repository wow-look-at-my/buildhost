package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
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

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main"})
	rec := httptest.NewRecorder()
	h.ServeRedirect(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/sites/mysite/branch/main/", rec.Header().Get("Location"))
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
		"index.html":     "<h1>root</h1>",
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
