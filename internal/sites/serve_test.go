package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

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
