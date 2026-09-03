package sites

// Zip upload tests: zip archives are converted to the internal tar form on
// upload (zipToTar), served like any site, and validated against traversal.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
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
	t.Serial()
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

func TestZipToTar_AbsolutePath(t *testing.T) {
	t.Serial()
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
