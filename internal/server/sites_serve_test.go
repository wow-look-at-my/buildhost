package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"testing"

	"github.com/wow-look-at-my/testify/require"
)

// TestSitesServedFileCSP proves, through the full server middleware chain, that
// a hosted static site's assets are served with a CSP that lets the page load
// them. securityHeaders applies "default-src 'none'" to every response (correct
// for the JSON/binary API); without the Serve handler overriding it, the browser
// blocks the site's own scripts/styles and the page renders blank.
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
	// The site CSP, not the server-wide "default-src 'none'".
	require.Equal(t, "default-src 'self' data:", resp.Header.Get("Content-Security-Policy"))
}
