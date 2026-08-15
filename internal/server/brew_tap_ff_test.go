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
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/server"
)

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// gitScratchDir is t.TempDir() for a git working tree: a scratch dir whose
// removal tolerates a transient concurrent creator.
//
// A working tree is touched by a tree of git subprocesses, and on a loaded
// runner an entry has been observed appearing inside .git between
// t.TempDir()'s final readdir and its rmdir -- Go's RemoveAll surfaces exactly
// that as "unlinkat ...: directory not empty" (its source returns the parent's
// ENOTEMPTY only when every child was removed cleanly), and t.TempDir() then
// fails a test whose every assertion passed. Which subprocess does it is not
// established: git's auto-maintenance is the obvious suspect and is ruled out
// -- these clones pin fetch.unpackLimit=1, so transfers stay packed, and
// `git maintenance run --auto` spawns no gc below the 6700-loose-object
// threshold.
//
// Retrying is right regardless of the culprit: this is the git CLIENT's
// scratch space, nothing buildhost owns is written here, so no product
// invariant can hide behind it -- a test's verdict must come from its
// assertions, not from temp-dir hygiene. A removal that never succeeds is
// logged rather than silently dropped.
func gitScratchDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "buildhost-git")
	require.NoError(t, err)
	t.Cleanup(func() {
		var err error
		for range 5 {
			if err = os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Logf("git scratch dir %s not removed: %v", dir, err)
	})
	return dir
}

// gitTestEnv is the environment every git invocation in these tests runs with.
// The identity vars keep commits reproducible; the GIT_CONFIG_* pair turns OFF
// automatic repacking, which fetch, rebase and clone all trigger. Auto-gc runs
// detached, so it can repack and delete a pack while a later `git fsck` in the
// same test is still enumerating -- surfacing as "packfile ... index not
// opened" / "unable to load rev-index for pack" on a tree nothing is wrong
// with. GIT_CONFIG_* rather than `-c` so the setting reaches the child
// processes git spawns for itself.
var gitTestEnv = []string{
	"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@test",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_CONFIG_COUNT=2",
	"GIT_CONFIG_KEY_0=gc.auto", "GIT_CONFIG_VALUE_0=0",
	"GIT_CONFIG_KEY_1=maintenance.auto", "GIT_CONFIG_VALUE_1=false",
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitTestEnv...)
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

	clone := filepath.Join(gitScratchDir(t), "tap")
	gitRun(t, gitScratchDir(t), "clone", gitTS.URL+"/brew/tap.git", clone)
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
