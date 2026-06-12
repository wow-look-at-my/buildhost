package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSitesServedFileCSP proves, through the full server middleware chain, that
// a hosted static site's assets are served without a blocking CSP. The global
// securityHeaders middleware applies "default-src 'none'" to every API response
// (correct for JSON/binary endpoints); the sites Serve handler removes it so
// the browser can load the site's own scripts, styles, and images.
func TestSitesServedFileCSP(t *testing.T) {
	env := setup(t)

	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects", `{"name":"mysite","versioning":"auto"}`).StatusCode)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	asset := []byte("console.log(1)")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "app.js", Size: int64(len(asset)), Mode: 0o644, Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(asset)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())

	resp := env.doSubdomainRequest(t, "PUT", "sites", "/mysite/branch/main", "application/gzip", bytes.NewReader(buf.Bytes()), true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.doSubdomainRequest(t, "GET", "sites", "/mysite/branch/main/app.js", "", nil, false)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// The global "default-src 'none'" CSP must be absent on site responses so
	// the page can load its own assets. The Serve handler removes it.
	require.Empty(t, resp.Header.Get("Content-Security-Policy"))
}
