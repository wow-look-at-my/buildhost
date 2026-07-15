package server_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
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

var (
	placeholderRE = regexp.MustCompile(`\{[^}]*\}`)
)

func TestLLMsTxt_PublicAndRendersBaseURL(t *testing.T) {
	env := setup(t)

	resp := env.get(t, "/llms.txt")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

	body := string(readBody(t, resp))
	require.Contains(t, body, "# buildhost")
	// Service URLs are subdomains of the base host (dl.{host}), not paths.
	require.Contains(t, body, strings.Replace(env.ts.URL, "://", "://dl.", 1))
	require.NotContains(t, body, "__BASE_URL__")
	require.NotContains(t, body, "__DL_URL__")
}

// llmsTxtSubdomains are every service subdomain that must serve /llms.txt
// directly (in addition to the apex). The router's strict host partitioning
// means a known subdomain never falls through to the host-agnostic apex route,
// so /llms.txt has to be registered on each subdomain too.
var llmsTxtSubdomains = []string{"apt", "brew", "dl", "git", "npm", "oci", "sites", "static"}

func TestLLMsTxt_ServedOnEverySubdomain(t *testing.T) {
	env := setup(t)

	for _, sub := range llmsTxtSubdomains {
		t.Run(sub, func(t *testing.T) {
			resp := env.getSubdomain(t, sub, "/llms.txt")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))

			body := string(readBody(t, resp))
			require.Contains(t, body, "# buildhost")
			// The guide's service URLs always anchor to the apex (test.local),
			// regardless of which subdomain served it -- never a double-prefixed
			// host such as dl.oci.test.local.
			require.Contains(t, body, "https://dl.test.local/myapp")
			require.Contains(t, body, "docker pull oci.test.local/myapp:latest")
			require.NotContainsf(t, body, "."+sub+".test.local",
				"service URLs leaked the %q request subdomain (double-prefixed host)", sub)
			require.NotContains(t, body, "__BASE_URL__")
		})
	}
}

// docker.{domain} is the registry's legacy alias and 301-redirects everything,
// including /llms.txt, to the canonical oci.{domain}.
func TestLLMsTxt_DockerSubdomainRedirectsToOCI(t *testing.T) {
	env := setup(t)

	resp := env.getSubdomain(t, "docker", "/llms.txt")
	defer resp.Body.Close()
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	require.Equal(t, "https://oci.test.local/llms.txt", resp.Header.Get("Location"))
}

func TestLLMsTxt_DocumentedRoutesAreRegistered(t *testing.T) {
	env := setup(t)

	body := string(readBody(t, env.get(t, "/llms.txt")))
	paths := documentedPaths(body, env.ts.URL)
	require.NotEmpty(t, paths, "expected to extract documented endpoints from /llms.txt")

	allRoutes := auth.AllRoutes()
	for _, p := range paths {
		if strings.HasPrefix(p, "/api/") || p == "/healthz" || p == "/llms.txt" {
			registered := false
			for _, route := range allRoutes {
				if routePatternMatches(route.Pattern, p) {
					registered = true
					break
				}
			}
			require.Truef(t, registered,
				"/llms.txt documents %q but no route is registered for it", p)
		}
	}
}

func TestLLMsTxt_DocumentedFlowsWork(t *testing.T) {
	env := setup(t)
	seedPublishedRelease(t, env)

	cases := []struct {
		name      string
		method    string
		subdomain string
		path      string
		auth      bool
		want      int
	}{
		{"llms.txt", "GET", "", "/llms.txt", false, http.StatusOK},
		{"healthz", "GET", "", "/healthz", false, http.StatusOK},
		{"list projects", "GET", "", "/api/v1/projects", true, http.StatusOK},
		{"download latest", "GET", "dl", "/myapp?os=linux&arch=amd64", false, http.StatusFound},
		{"download explicit version", "GET", "dl", "/myapp?v=1&os=linux&arch=amd64", false, http.StatusMovedPermanently},
		{"download branch", "GET", "dl", "/myapp?branch=main&os=linux&arch=amd64", false, http.StatusFound},
		{"download tar.gz", "GET", "dl", "/myapp?os=linux&arch=amd64&fmt=tar.gz", false, http.StatusFound},
		{"static rejects latest", "GET", "static", "/file?arch=amd64&fmt=raw&os=linux&project=myapp&v=latest", false, http.StatusBadRequest},
		{"brew formula", "GET", "brew", "/myapp", false, http.StatusOK},
		{"apt Release", "GET", "apt", "/myapp/dists/stable/Release", false, http.StatusOK},
		{"npm metadata", "GET", "npm", "/@buildhost/myapp", false, http.StatusOK},
		// /v2/ is the OCI auth-discovery endpoint: anonymous -> 401 + challenge,
		// authenticated -> 200. The documented "docker login then pull" flow
		// reaches it with credentials. (The anonymous 401 challenge is covered by
		// the oci package's V2Root unit tests.)
		{"oci v2 root", "GET", "oci", "/v2/", true, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp *http.Response
			if tc.subdomain != "" {
				resp = env.doSubdomainRequest(t, tc.method, tc.subdomain, tc.path, "", nil, tc.auth)
			} else {
				resp = env.doRequest(t, tc.method, tc.path, "", nil, tc.auth)
			}
			defer resp.Body.Close()
			require.Equalf(t, tc.want, resp.StatusCode, "%s %s", tc.method, tc.path)
		})
	}
}

func seedPublishedRelease(t *testing.T, env *testEnv) {
	t.Helper()
	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects", `{"name":"myapp","versioning":"auto"}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects/myapp/releases", `{"git_branch":"master","git_commit":"abc123"}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.putBody(t, "/api/v1/projects/myapp/releases/1/artifacts/linux/amd64", []byte("#!/bin/sh\necho hi\n")).StatusCode)
	require.Equal(t, http.StatusOK,
		env.postJSON(t, "/api/v1/projects/myapp/releases/1/publish", `{}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects/myapp/releases", `{"git_branch":"main","git_commit":"def456"}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.putBody(t, "/api/v1/projects/myapp/releases/2/artifacts/linux/amd64", []byte("#!/bin/sh\necho branch\n")).StatusCode)
	require.Equal(t, http.StatusOK,
		env.postJSON(t, "/api/v1/projects/myapp/releases/2/publish", `{}`).StatusCode)
}

func documentedPaths(body, baseURL string) []string {
	absURLRE := regexp.MustCompile(regexp.QuoteMeta(baseURL) + `(/[^\s"'<>)]*)`)

	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		p = strings.TrimRight(p, ".,`")
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
	return out
}
