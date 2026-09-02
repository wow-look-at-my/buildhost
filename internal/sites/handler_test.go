package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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

// The publish response must name the canonical URL for the branch it deployed,
// so no publisher has to reimplement the grammar to advertise a site.
func TestUpload_ResponseCarriesCanonicalURL(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	require.NoError(t, d.SetProjectDefaultBranch(context.Background(), proj.ID, "master"))
	proj.DefaultBranch = "master"

	publish := func(branch string) string {
		t.Helper()
		body := makeTarGz(t, map[string]string{"index.html": "<h1>hi</h1>"})
		req := httptest.NewRequest("PUT", "/sites/mysite/branch/"+branch, bytes.NewReader(body))
		req = withRoute(req, proj, route{project: "mysite", branch: branch, write: true})
		rec := httptest.NewRecorder()
		h.Upload(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var got struct {
			db.Site
			URL string `json:"url"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
		// The embedded row's fields stay top-level, so an older client that
		assert.Equal(t, branch, got.Branch)
		return got.URL
	}

	// The default branch's deployment IS the bare project path.
	assert.Equal(t, "https://sites.example.com/mysite/", publish("master"))
	// Any other branch needs naming, and the "@" form is how it is named --
	assert.Equal(t, "https://sites.example.com/mysite/@pr-7/", publish("pr-7"))
}
