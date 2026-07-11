package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/server"
)

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@test",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// publishBrewProject drives the public API end to end: project + release +
// linux/amd64 binary artifact + publish.
func publishBrewProject(t *testing.T, env *testEnv, name, body string) {
	t.Helper()
	resp := env.postJSON(t, "/api/v1/projects", fmt.Sprintf(`{"name":%q,"versioning":"auto"}`, name))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/"+name+"/releases", `{"git_branch":"master","git_commit":"abc"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.putBody(t, "/api/v1/projects/"+name+"/releases/1/artifacts/linux/amd64", []byte(body))
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/"+name+"/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// The tap history regression test through the REAL router: a real `git clone`
// of the generated tap, a publish, a redeploy (fresh server.New over the same
// data dir -- which re-runs every OnReady wiring, including the tap cache
// reset), and then exactly Homebrew's update sequence (`git fetch --force` +
// `git rebase origin/main`). It must fast-forward with zero conflicts and the
// new tip must descend from the old one -- the old behavior minted an
// unrelated root commit per build and wedged every client mid-rebase.
func TestBrewTap_GitUpdateFastForwardsAcrossPublishAndRedeploy(t *testing.T) {
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")

	// Serve the same router under the git service host, so a real git client
	// can reach git.{domain} through a plain URL.
	gitTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "git.test.local"
		env.handler.ServeHTTP(w, r)
	}))
	t.Cleanup(gitTS.Close)

	clone := filepath.Join(t.TempDir(), "tap")
	gitRun(t, t.TempDir(), "clone", gitTS.URL+"/brew/tap.git", clone)
	tip1 := strings.TrimSpace(gitRun(t, clone, "rev-parse", "origin/main"))

	publishBrewProject(t, env, "apptwo", "apptwo-binary")

	// Redeploy: re-wiring over the same data dir must preserve the persisted
	// tap history (and clears the in-TTL cache, so the next request rebuilds).
	server.New(env.cfg, env.database, env.store)

	gitRun(t, clone, "fetch", "--force", "origin")
	gitRun(t, clone, "rebase", "origin/main")

	tip2 := strings.TrimSpace(gitRun(t, clone, "rev-parse", "origin/main"))
	require.NotEqual(t, tip1, tip2)
	gitRun(t, clone, "merge-base", "--is-ancestor", tip1, tip2)
	require.Equal(t, tip2, strings.TrimSpace(gitRun(t, clone, "rev-parse", "HEAD")))
	require.Empty(t, strings.TrimSpace(gitRun(t, clone, "status", "--porcelain")))
	_, err := os.Stat(filepath.Join(clone, "Formula", "apptwo.rb"))
	require.NoError(t, err)
}

// A slash-namespaced project's formula lives in the tap under its FOLDED
// filename (gcc/pgo -> gcc-pgo.rb); the per-formula URL users copy must
// resolve that folded name back to the project instead of 404ing.
func TestBrewFormula_FoldedFilenameResolvesSlashNamespacedProject(t *testing.T) {
	env := setup(t)
	publishBrewProject(t, env, "gcc/pgo", "pgo-binary")

	resp := env.getSubdomain(t, "brew", "/Formula/gcc-pgo.rb")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := string(readBody(t, resp))
	require.Contains(t, body, "class GccPgo < Formula")
	require.Contains(t, body, "depends_on :linux")

	// An unfoldable name still 404s.
	resp = env.getSubdomain(t, "brew", "/Formula/no-such-project.rb")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}
