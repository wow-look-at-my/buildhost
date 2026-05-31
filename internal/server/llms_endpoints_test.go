package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/testify/require"
)

func routePatternMatches(pattern, path string) bool {
	patSegs := strings.Split(strings.TrimRight(pattern, "/"), "/")
	pathSegs := strings.Split(strings.TrimRight(path, "/"), "/")
	for i, ps := range patSegs {
		if strings.HasPrefix(ps, "{") {
			if strings.HasSuffix(ps, "...}") {
				return true
			}
			continue
		}
		if i >= len(pathSegs) || ps != pathSegs[i] {
			return false
		}
	}
	return len(patSegs) == len(pathSegs) ||
		(strings.HasSuffix(pattern, "/") && len(pathSegs) >= len(patSegs))
}

// These tests guard against documentation drift in the /llms.txt endpoint.
// The document advertises a hand-written list of buildhost URLs; without a
// guardrail those can silently rot as routes change. Two layers protect the
// document, and both run as part of the standard `go-toolchain` CI check that
// gates merge:
//
//  1. TestLLMsTxt_DocumentedRoutesAreRegistered parses the *served* document
//     and asserts every URL it references resolves to a route registered on
//     the server mux. A typo'd or removed endpoint fails the build.
//  2. TestLLMsTxt_DocumentedFlowsWork exercises the primary documented flows
//     end to end against a seeded server, proving they actually respond as the
//     document claims (downloads redirect, package-manager formats render, the
//     /static latest-rejection behaves as documented, etc.).

var (
	placeholderRE = regexp.MustCompile(`\{[^}]*\}`)
	methodPathRE  = regexp.MustCompile(`(?m)^\s*(?:GET|POST|PUT|DELETE)\s+(/\S+)`)
)

func TestLLMsTxt_PublicAndRendersBaseURL(t *testing.T) {
	env := setup(t)

	// No auth token: the endpoint must be publicly reachable.
	resp := env.get(t, "/llms.txt")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := string(readBody(t, resp))
	require.Contains(t, body, "# buildhost")
	// The configured base URL ("http://localhost") is substituted into examples.
	require.Contains(t, body, "http://localhost/dl/myapp/latest/linux/amd64")
	require.NotContains(t, body, "__BASE_URL__")
}

func TestLLMsTxt_DocumentedRoutesAreRegistered(t *testing.T) {
	env := setup(t) // boots the server, registering /healthz on the shared mux

	body := string(readBody(t, env.get(t, "/llms.txt")))
	paths := documentedPaths(body, "http://localhost")
	require.NotEmpty(t, paths, "expected to extract documented endpoints from /llms.txt")

	routes := auth.Router().Routes()
	for _, p := range paths {
		registered := false
		for _, route := range routes {
			if routePatternMatches(route.Pattern, p) {
				registered = true
				break
			}
		}
		require.Truef(t, registered,
			"/llms.txt documents %q but no route is registered for it", p)
	}
}

func TestLLMsTxt_DocumentedFlowsWork(t *testing.T) {
	env := setup(t)
	seedPublishedRelease(t, env) // myapp, release 1, linux/amd64, published

	// Each documented surface must respond as advertised with real data.
	cases := []struct {
		name   string
		method string
		path   string
		auth   bool
		want   int
	}{
		{"llms.txt", "GET", "/llms.txt", false, http.StatusOK},
		{"healthz", "GET", "/healthz", false, http.StatusOK},
		{"list projects", "GET", "/api/v1/projects", true, http.StatusOK},
		{"download latest", "GET", "/dl/myapp/latest/linux/amd64", false, http.StatusFound},
		{"download branch", "GET", "/dl/myapp/branch/main/linux/amd64", false, http.StatusFound},
		{"download tar.gz", "GET", "/dl/myapp/1/linux/amd64?format=tar.gz", false, http.StatusFound},
		// /static rejects v=latest (documented). Query is already canonical
		// (keys sorted) so the handler does not 301-canonicalize first.
		{"static rejects latest", "GET", "/static?arch=amd64&fmt=raw&id=myapp&os=linux&v=latest", false, http.StatusBadRequest},
		{"brew formula", "GET", "/brew/myapp.rb", false, http.StatusOK},
		{"apt Release", "GET", "/apt/myapp/dists/stable/Release", false, http.StatusOK},
		{"npm metadata", "GET", "/npm/@buildhost/myapp", false, http.StatusOK},
		{"oci v2 root", "GET", "/v2/", false, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.doRequest(t, tc.method, tc.path, "", nil, tc.auth)
			defer resp.Body.Close()
			require.Equalf(t, tc.want, resp.StatusCode, "%s %s", tc.method, tc.path)
		})
	}
}

// documentedPaths extracts every buildhost request path referenced in the
// rendered /llms.txt body, with {placeholders} replaced by a sample segment so
// they can be matched against the server's registered route patterns. It picks
// up both absolute example URLs (baseURL + path) and the method-prefixed
// relative paths in the REST API reference block.
func documentedPaths(body, baseURL string) []string {
	absURLRE := regexp.MustCompile(regexp.QuoteMeta(baseURL) + "(/[^\\s\"'`<>)]*)")

	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		p = strings.TrimRight(p, ".,")
		if !strings.HasPrefix(p, "/") {
			return
		}
		p = placeholderRE.ReplaceAllString(p, "x")
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, m := range absURLRE.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	for _, m := range methodPathRE.FindAllStringSubmatch(body, -1) {
		add(m[1])
	}
	return out
}

// seedPublishedRelease creates project "myapp" with a published linux/amd64
// release, matching the published state the /llms.txt examples assume.
func seedPublishedRelease(t *testing.T, env *testEnv) {
	t.Helper()
	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects", `{"name":"myapp","versioning":"auto"}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects/myapp/releases", `{"git_branch":"main","git_commit":"abc123"}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.putBody(t, "/api/v1/projects/myapp/releases/1/artifacts/linux/amd64", []byte("#!/bin/sh\necho hi\n")).StatusCode)
	require.Equal(t, http.StatusOK,
		env.postJSON(t, "/api/v1/projects/myapp/releases/1/publish", `{}`).StatusCode)
}
