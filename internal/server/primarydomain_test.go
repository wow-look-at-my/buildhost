package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"

	// The web frontend registers its routes (GET /, /projects/*, /_ui/...) in
	// its init(); the rest of this package's tests never exercise them, so the
	// backend is imported only here.
	_ "github.com/wow-look-at-my/buildhost/internal/web"
)

// Primary-apex scoping of the main-domain UI/API surface: with
// BUILDHOST_PRIMARY_DOMAIN configured, the web frontend and /api/v1 answer
// ONLY on that apex. On any other unclaimed host they serve the router's
// canonical 404, indistinguishable from a route that does not exist, while the
// deliberately host-agnostic set (/healthz, /ready-to-update, /llms.txt,
// /__signin, /__sso) keeps answering everywhere.

func TestPrimaryDomain_ScopesWebAndAPIToApex(t *testing.T) {
	env := setupSiteDomain(t, siteTestDomain, primaryTestDomain, true)

	// API writes address the primary apex (doRequest sets the Host) -- this is
	// the served-on-primary write case.
	env.createProject(t, "scoped-app", false)

	// Web + API are served on the primary apex.
	resp, body := siteGet(t, env, primaryTestDomain, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	assert.Contains(t, body, "scoped-app")

	resp, _ = siteGet(t, env, primaryTestDomain, "/projects/scoped-app")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, _ = siteGet(t, env, primaryTestDomain, "/_ui/style.css")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body = siteGet(t, env, primaryTestDomain, "/api/v1/projects")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "scoped-app")

	// The SAME paths on an unknown host, and on the bare site apex, are 404:
	// the UI/API does not exist off the primary apex.
	for _, host := range []string{"evil.example", siteTestDomain} {
		for _, path := range []string{"/", "/projects/scoped-app", "/_ui/style.css", "/api/v1/projects"} {
			resp, _ := siteGet(t, env, host, path)
			require.Equalf(t, http.StatusNotFound, resp.StatusCode,
				"GET %s%s must 404 off the primary apex", host, path)
		}
	}

	// The off-apex 404 is byte-identical to the router's 404 for a path that
	// was never registered, so a probe cannot tell "wrong host" from "no such
	// route".
	respScoped, bodyScoped := siteGet(t, env, "evil.example", "/")
	respNoRoute, bodyNoRoute := siteGet(t, env, "evil.example", "/definitely/not/registered")
	require.Equal(t, http.StatusNotFound, respScoped.StatusCode)
	require.Equal(t, http.StatusNotFound, respNoRoute.StatusCode)
	assert.Equal(t, bodyNoRoute, bodyScoped)
	assert.Equal(t, respNoRoute.Header.Get("Content-Type"), respScoped.Header.Get("Content-Type"))

	// An authenticated API write off-apex gets the same plain 404 before any
	// auth semantics run -- nothing is created.
	resp = env.doFullHost(t, "POST", "evil.example", "/api/v1/projects", "application/json",
		nil, strings.NewReader(`{"name":"evil-made","versioning":"auto"}`), true)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, body = siteGet(t, env, primaryTestDomain, "/api/v1/projects")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, body, "evil-made")

	// The host-agnostic keep-set still answers on an unknown host.
	resp, _ = siteGet(t, env, "evil.example", "/healthz")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = siteGet(t, env, "evil.example", "/ready-to-update")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// docker-updater probes the container by IP, i.e. on an unclaimed host, and
	// carries no credential: both standard endpoints have to answer there. The
	// paths are spelled out rather than taken from the server package's consts
	// -- they are an external contract, and a rename that silently moved them
	// would be exactly the regression this pins.
	resp, _ = siteGet(t, env, "evil.example", "/.well-known/docker-updater/health")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = siteGet(t, env, "evil.example", "/.well-known/docker-updater/pre-update")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, _ = siteGet(t, env, "evil.example", "/llms.txt")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp = env.doFullHost(t, "GET", "evil.example", "/__signin", "", nil, nil, false)
	resp.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "github.com/login/oauth/authorize")

	// The bare site apex loses web/API (asserted above) but keeps the
	// fallthrough the sign-in flow relies on: /healthz and /__sso redemption.
	resp, _ = siteGet(t, env, siteTestDomain, "/healthz")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, body = siteGet(t, env, siteTestDomain, "/__sso?code=garbage")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body, "Sign-in failed")

	// Service subdomains ride host-bearing routes and are unaffected on any
	// domain.
	resp, _ = siteGet(t, env, "oci.evil.example", "/llms.txt")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// With no primary domain configured the main-domain surface stays fully
// host-agnostic -- the historical behavior, pinned so the scoping is provably
// opt-in -- and configuring one changes serve-time behavior only, never the
// route table.
func TestPrimaryDomain_UnsetKeepsHostAgnostic(t *testing.T) {
	env := setup(t)
	env.createProject(t, "anyhost-app", false)

	for _, host := range []string{"evil.example", "some.random.host"} {
		resp, _ := siteGet(t, env, host, "/")
		require.Equalf(t, http.StatusOK, resp.StatusCode, "GET %s/ with no primary domain", host)
		resp, body := siteGet(t, env, host, "/api/v1/projects")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, body, "anyhost-app")
		resp, _ = siteGet(t, env, host, "/projects/anyhost-app")
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// Primary scoping is a serve-time gate on existing registrations: an Init
	// WITH a primary domain registers zero new routes.
	base := routePatterns()
	setupWith(t, func(cfg *config.Config) { cfg.PrimaryDomain = "route-pin.example" })
	require.Equal(t, base, routePatterns(),
		"configuring a primary domain must not change the route table")
}
