package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/go-containers/set"
)

// The {project}.<site-domain> serving scheme, end to end through the full
// server stack: config -> auth.Init -> conditional route registration -> host
// dispatch -> requireProject -> sites handlers.

const (
	siteTestDomain    = "site.example"
	primaryTestDomain = "primary.example"
)

func setupSiteDomain(t *testing.T, siteDomain, primaryDomain string, ghLogin bool) *testEnv {
	t.Helper()
	return setupWith(t, func(cfg *config.Config) {
		cfg.SiteDomain = siteDomain
		cfg.PrimaryDomain = primaryDomain
		if ghLogin {
			cfg.GitHubClientID = "test-client-id"
			cfg.GitHubClientSecret = "test-client-secret"
		}
	})
}

// doFullHost issues a request against the test server with an arbitrary Host
// header (doSubdomainRequest always appends .test.local) and optional extra
// headers.
func (e *testEnv) doFullHost(t *testing.T, method, host, path, contentType string, headers map[string]string, body io.Reader, authed bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, body)
	require.NoError(t, err)
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if authed {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func makeSiteTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Size: int64(len(content)), Mode: 0o644, Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// createProject creates a project via the REST API (is_private optional).
func (e *testEnv) createProject(t *testing.T, name string, private bool) {
	t.Helper()
	body := `{"name":"` + name + `","versioning":"auto"}`
	if private {
		body = `{"name":"` + name + `","versioning":"auto","is_private":true}`
	}
	resp := e.postJSON(t, "/api/v1/projects", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
}

// uploadBranchSite deploys a site to a branch via the classic
// sites.{domain} PUT (the only write path; the subdomain scheme is read-only).
func (e *testEnv) uploadBranchSite(t *testing.T, project, branch string, public bool, files map[string]string) {
	t.Helper()
	headers := map[string]string{}
	if public {
		headers["X-Public-Site"] = "true"
	}
	resp := e.doFullHost(t, "PUT", "sites.test.local", "/"+project+"/branch/"+branch, "application/gzip",
		headers, bytes.NewReader(makeSiteTarGz(t, files)), true)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "upload %s@%s", project, branch)
	resp.Body.Close()
}

func siteGet(t *testing.T, e *testEnv, host, path string) (*http.Response, string) {
	t.Helper()
	resp := e.doFullHost(t, "GET", host, path, "", nil, nil, false)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	return resp, string(b)
}

func routePatterns() []string {
	var out []string
	for _, r := range auth.AllRoutes() {
		out = append(out, r.String())
	}
	sort.Strings(out)
	return out
}

// With BUILDHOST_SITE_DOMAIN unset, an Init registers ZERO new routes -- the
// route table is byte-identical to a deployment without the feature. With it
// set, exactly the documented routes appear.
func TestSiteDomain_RouteTable(t *testing.T) {
	setup(t) // reach registration steady state (route registration is sticky per process)
	base := routePatterns()

	setup(t) // a second unset Init must add nothing
	require.Equal(t, base, routePatterns(),
		"an Init without BUILDHOST_SITE_DOMAIN must register zero new routes")

	// A dedicated domain no other test uses, so the diff is exact.
	setupSiteDomain(t, "routetable.example", "", false)
	after := routePatterns()

	var added []string
	inBase := set.Of(base...)
	for _, p := range after {
		if !inBase.Contains(p) {
			added = append(added, p)
		}
	}
	// The per-domain routes are always new; the host-agnostic /__sso is shared
	// across configured domains, so an earlier site-domain test in this process
	// may have already registered it.
	require.Subset(t, added, []string{
		"{project}.routetable.example/__sso {GET}",
		"{project}.routetable.example/{path...} {GET}",
	})
	for _, p := range added {
		require.Contains(t, []string{
			"/__sso {GET}",
			"{project}.routetable.example/__sso {GET}",
			"{project}.routetable.example/{path...} {GET}",
		}, p, "unexpected route registered by the site-domain feature")
	}
	require.Contains(t, after, "/__sso {GET}")

	setup(t) // and unset again: still nothing new beyond the configured set
	require.Equal(t, after, routePatterns())
}

func TestSiteDomain_DispatchAndServe(t *testing.T) {
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, false)

	env.createProject(t, "myapp", false)
	env.uploadBranchSite(t, "myapp", "master", false, map[string]string{
		"index.html":     "root-master",
		"docs/page.html": "docs-page",
	})

	// Bare path serves the default branch.
	resp, body := siteGet(t, env, "myapp."+siteTestDomain, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "root-master", body)
	// Site security headers apply on the subdomain scheme: the global CSP is
	// dropped so the site's own assets load.
	assert.Empty(t, resp.Header.Get("Content-Security-Policy"))

	resp, body = siteGet(t, env, "myapp."+siteTestDomain, "/docs/page.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "docs-page", body)

	// A project named like a service label is a PROJECT on the site domain: the
	// {project}.<site-domain> family (2 literal host labels) claims the host
	// over dl.{domain} (1). Only the site scheme can serve this content.
	env.createProject(t, "dl", false)
	env.uploadBranchSite(t, "dl", "master", false, map[string]string{"index.html": "dl-site"})
	resp, body = siteGet(t, env, "dl."+siteTestDomain, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "dl-site", body)

	// The BARE site apex (2 labels) matches no host-bearing route and falls
	// through to host-agnostic routes.
	resp, body = siteGet(t, env, siteTestDomain, "/healthz")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, `"status":"ok"`)

	// /__sso on a project subdomain reaches the SSO handler (literal path beats
	// the {path...} catch-all), not the sites handler: a bogus code gets the
	// HTML failure page, not a JSON project-404.
	resp, body = siteGet(t, env, "nosuch."+siteTestDomain, "/__sso?code=garbage")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	assert.Contains(t, body, "Sign-in failed")
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))

	// llms.txt documents the scheme when configured.
	resp, body = siteGet(t, env, "test.local", "/llms.txt")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "myapp."+siteTestDomain)
	assert.NotContains(t, body, "__SITE_SECTION__")
}

func TestSiteDomain_Visibility(t *testing.T) {
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, true)

	env.createProject(t, "secret", true)
	env.uploadBranchSite(t, "secret", "master", false, map[string]string{"index.html": "top-secret"})
	env.uploadBranchSite(t, "secret", "preview", true, map[string]string{"index.html": "public-preview"})

	host := "secret." + siteTestDomain

	// The public branch serves anonymously even under the private project.
	resp, body := siteGet(t, env, host, "/@preview/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "public-preview", body)

	// A private branch: programmatic client gets the JSON 401.
	resp, _ = siteGet(t, env, host, "/")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	// A browser is 303'd to the PRIMARY apex sign-in with the full original URL.
	resp = env.doFullHost(t, "GET", host, "/", "", map[string]string{"Accept": "text/html"}, nil, false)
	resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t,
		"https://"+primaryTestDomain+"/__signin?next="+url.QueryEscape("https://"+host+"/"),
		resp.Header.Get("Location"))

	// An authorized token still reads the private branch on the subdomain scheme.
	resp = env.doFullHost(t, "GET", host, "/", "", nil, nil, true)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "top-secret", string(b))

	// Gate and serve agree on the resolved default branch: default_branch is the
	// seed "master" but the only site lives on "main" -- resolveRootBranch falls
	// back to it, in AllowsPublicRead and in the handler alike.
	env.createProject(t, "agreep", true)
	env.uploadBranchSite(t, "agreep", "main", true, map[string]string{"index.html": "main-content"})
	resp, body = siteGet(t, env, "agreep."+siteTestDomain, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "main-content", body)
}

// Without a configured primary domain the cross-domain sign-in hop does not
// exist: a site-domain browser gets the plain JSON 401 (graceful degradation),
// never a redirect to an apex that cannot complete OAuth.
func TestSiteDomain_NoPrimaryDomain_Browser401(t *testing.T) {
	env := setupSiteDomain(t, "nopri.example", "", true)

	env.createProject(t, "secret2", true)
	env.uploadBranchSite(t, "secret2", "master", false, map[string]string{"index.html": "x"})

	resp := env.doFullHost(t, "GET", "secret2.nopri.example", "/", "", map[string]string{"Accept": "text/html"}, nil, false)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Location"))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

// Slash-named branches on the CLASSIC scheme: {branch} binds only the first
// path segment, so branch claude/foo uploaded fine (greedy PUT bind) but every
// GET 404'd. Serve-side longest-match fixes fetch; both-branches-exist prefers
// the longest.
func TestSiteDomain_SlashBranchClassicScheme(t *testing.T) {
	env := setup(t) // no site domain needed: this is the classic-scheme fix

	env.createProject(t, "p2", false)
	// A site on the default branch, so the slash-named branches under test stay
	// non-default refs and are served at their own URLs rather than collapsing
	// into the bare project path.
	env.uploadBranchSite(t, "p2", "master", false, map[string]string{"index.html": "default"})
	env.uploadBranchSite(t, "p2", "claude/foo", false, map[string]string{
		"index.html": "cf", "f.js": "cf-f",
	})

	// Regression: these were 404 before the fix. A slash-named branch is
	// addressed with the "@" sigil, which marks where the ref STARTS -- the
	// remainder is still split by longest match against the project's sites.
	resp, body := siteGet(t, env, "sites.test.local", "/p2/@claude/foo/index.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "cf", body)
	resp, body = siteGet(t, env, "sites.test.local", "/p2/@claude/foo/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "cf", body)

	// Branch root canonicalization still applies to the full slashed branch.
	resp, _ = siteGet(t, env, "sites.test.local", "/p2/@claude/foo")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	assert.Equal(t, "/p2/@claude/foo/", resp.Header.Get("Location"))

	// The legacy spelling resolves the same slash-named branch and 302s to it,
	// naming the branch it resolved -- in one hop, not a chain.
	resp, _ = siteGet(t, env, "sites.test.local", "/p2/branch/claude/foo/index.html")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/p2/@claude/foo/index.html", resp.Header.Get("Location"))

	// With BOTH claude and claude/foo, each file serves from its own branch.
	env.uploadBranchSite(t, "p2", "claude", false, map[string]string{"a.html": "c-a"})
	resp, body = siteGet(t, env, "sites.test.local", "/p2/@claude/a.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "c-a", body)
	resp, body = siteGet(t, env, "sites.test.local", "/p2/@claude/foo/f.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "cf-f", body)

	// The public-read gate resolves the same way (private project, public
	// slash-named branch, anonymous read). claude/foo is p3's only site, so it
	// is also its default branch -- the canonical URL is the bare project path.
	env.createProject(t, "p3", true)
	env.uploadBranchSite(t, "p3", "claude/foo", true, map[string]string{"index.html": "pub-cf"})
	resp, body = siteGet(t, env, "sites.test.local", "/p3/index.html")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "pub-cf", body)

	// The gate opens the legacy URL for that same public branch too: it
	// redirects rather than 401ing, so an old shared link still lands.
	resp, _ = siteGet(t, env, "sites.test.local", "/p3/branch/claude/foo/index.html")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "/p3/index.html", resp.Header.Get("Location"))
}

func TestSiteUploadValidation(t *testing.T) {
	env := setup(t)
	env.createProject(t, "p4", false)

	payload := makeSiteTarGz(t, map[string]string{"index.html": "x"})

	put := func(branch string) *http.Response {
		return env.doFullHost(t, "PUT", "sites.test.local", "/p4/branch/"+branch, "application/gzip",
			nil, bytes.NewReader(payload), true)
	}

	// Characters outside [a-zA-Z0-9._/-] are rejected.
	resp := put("bad~name")
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp = put("bad%20name") // decodes to "bad name"
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Over-long branches are rejected.
	resp = put(strings.Repeat("b", 257))
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// A valid slash-named branch is still accepted.
	resp = put("claude/ok")
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// v1 DNS-label gate: only names valid as a single DNS label serve on the
// subdomain scheme; everything else stays classic-scheme-only.
func TestSiteDomain_DNSLabelGate(t *testing.T) {
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, false)

	long := strings.Repeat("l", 64)
	files := map[string]string{"index.html": "gated"}
	for _, name := range []string{"x_y", "bad-", "x.y", long, "a/b"} {
		env.createProject(t, name, false)
		env.uploadBranchSite(t, name, "master", false, files)
		// All of them serve on the classic scheme, at the canonical URL.
		resp, body := siteGet(t, env, "sites.test.local", "/"+name+"/")
		require.Equalf(t, http.StatusOK, resp.StatusCode, "classic serve for %q", name)
		assert.Equal(t, "gated", body)
	}

	// One-label hosts whose label is not a valid DNS label 404 on the scheme.
	for _, label := range []string{"x_y", "bad-", long} {
		resp, _ := siteGet(t, env, label+"."+siteTestDomain, "/")
		assert.Equalf(t, http.StatusNotFound, resp.StatusCode, "label %q must not serve", label)
	}

	// A dotted name is two host labels: the 3-label pattern does not match, the
	// host stays unclaimed, and host-agnostic routes (which have no such path)
	// answer 404. The project's site is NOT reachable there.
	resp, _ := siteGet(t, env, "x.y."+siteTestDomain, "/index.html")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
