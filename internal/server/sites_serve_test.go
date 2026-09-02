package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
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

	resp = env.doSubdomainRequest(t, "GET", "sites", "/mysite/app.js", "", nil, false)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// The global "default-src 'none'" CSP must be absent on site responses so
	require.Empty(t, resp.Header.Get("Content-Security-Policy"))
}

// A site file must be reachable at the project's own root path, through the
// whole stack: host dispatch -> requireProject -> sites handler. Before this,
// only /{project}/branch/{branch}/{path} served a file and the bare root merely
// redirected, so every link into a site had to name a branch -- and any link
func TestSitesApexPath(t *testing.T) {
	env := setup(t)

	env.createProject(t, "apexp", false)
	env.uploadBranchSite(t, "apexp", "main", false, map[string]string{
		"index.html":  "root",
		"runner.html": "runner",
		"a/b.css":     "nested",
		"404.html":    "missing",
	})

	// Files resolve under the project root, on the project's default branch
	// (default_branch is the seed "master" here, so this also exercises
	for path, want := range map[string]string{
		"/apexp/runner.html": "runner",
		"/apexp/a/b.css":     "nested",
		"/apexp/a/":          "missing",
	} {
		resp, body := siteGet(t, env, "sites.test.local", path)
		require.Equalf(t, want, body, "GET %s", path)
		if path == "/apexp/a/" {
			require.Equal(t, http.StatusNotFound, resp.StatusCode)
		} else {
			require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s", path)
		}
	}

	resp, body := siteGet(t, env, "sites.test.local", "/apexp/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "root", body)

	resp, _ = siteGet(t, env, "sites.test.local", "/apexp")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	require.Equal(t, "/apexp/", resp.Header.Get("Location"))

	// The @ spelling of that same default branch collapses INTO the bare URL.
	resp, _ = siteGet(t, env, "sites.test.local", "/apexp/@main/runner.html")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/apexp/runner.html", resp.Header.Get("Location"))
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))

	// The legacy branch route is unshadowed, and 302s to the canonical URL for
	resp, _ = siteGet(t, env, "sites.test.local", "/apexp/branch/main/runner.html")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/apexp/runner.html", resp.Header.Get("Location"))

	resp, body = siteGet(t, env, "sites.test.local", "/apexp/branches")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body, `"branch":"main"`)
}

// The apex file path is gated exactly like every other site read: the
// public-read bypass opens only a public site's own default branch, and the
// gate resolves that branch through the same helper the handler does.
func TestSitesApexPathVisibility(t *testing.T) {
	env := setup(t)

	env.createProject(t, "apexpriv", true)
	env.uploadBranchSite(t, "apexpriv", "master", false, map[string]string{"secret.txt": "top-secret"})

	resp, _ := siteGet(t, env, "sites.test.local", "/apexpriv/secret.txt")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = env.doFullHost(t, "GET", "sites.test.local", "/apexpriv/secret.txt", "", nil, nil, true)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "top-secret", string(body))

	// A public site under a private project serves its files anonymously at the
	env.createProject(t, "apexpub", true)
	env.uploadBranchSite(t, "apexpub", "main", true, map[string]string{"preview.html": "public-preview"})
	resp, text := siteGet(t, env, "sites.test.local", "/apexpub/preview.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "public-preview", text)

	// The "@" spelling is gated identically: the public branch serves
	resp, _ = siteGet(t, env, "sites.test.local", "/apexpub/@main/preview.html")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/apexpub/preview.html", resp.Header.Get("Location"))

	// The private project's @ URL is gated identically to its apex path: no
	resp, _ = siteGet(t, env, "sites.test.local", "/apexpriv/@master/secret.txt")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp = env.doFullHost(t, "GET", "sites.test.local", "/apexpriv/@master/secret.txt", "", nil, nil, true)
	resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "/apexpriv/secret.txt", resp.Header.Get("Location"))
}
