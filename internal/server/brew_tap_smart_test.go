package server_test

import (
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

// gitRunEnv is gitRun with extra environment variables (e.g. GIT_SMART_HTTP=0
// to force the dumb protocol).
func gitRunEnv(t *testing.T, dir string, extraEnv []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@test",
		"GIT_TERMINAL_PROMPT=0",
	), extraEnv...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// gitTapServer serves the real router under the git service host so a real
// git client can reach git.{domain} through a plain URL.
func gitTapServer(t *testing.T, env *testEnv) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "git.test.local"
		env.handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// hasPackFiles reports whether a clone's object store contains packfiles --
// with fetch.unpackLimit=1 pinned at clone time this is the tell that the
// transfer used the smart protocol: a pack transfer is kept as a pack, while
// a dumb-HTTP clone fetches loose objects only (this tap serves no pre-built
// packs). Without the pin, git quietly UNPACKS small received packs (below
// the default 100-object unpackLimit), which made pack presence useless as a
// protocol discriminator for a KB-scale tap.
func hasPackFiles(t *testing.T, clone string) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(clone, ".git", "objects", "pack"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pack") {
			return true
		}
	}
	return false
}

// The tap-history FF regression through the SMART path: the router's
// info/refs answers the smart content type (which is what makes real git pick
// smart automatically), the clone transfers the full history as one pack, and
// exactly Homebrew's update sequence (`git fetch --force` + `git rebase
// origin/main`) fast-forwards across a publish and a redeploy.
func TestBrewTapSmart_GitUpdateFastForwardsAcrossPublishAndRedeploy(t *testing.T) {
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	gitTS := gitTapServer(t, env)

	// The router really negotiates smart: this is the response that flips a
	// git client from the dumb to the smart protocol.
	resp, err := http.Get(gitTS.URL + "/brew/tap.git/info/refs?service=git-upload-pack")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/x-git-upload-pack-advertisement", resp.Header.Get("Content-Type"))
	resp.Body.Close()

	clone := filepath.Join(t.TempDir(), "tap")
	gitRun(t, t.TempDir(), "clone", "-c", "fetch.unpackLimit=1", gitTS.URL+"/brew/tap.git", clone)
	require.True(t, hasPackFiles(t, clone), "a smart clone stores the transferred pack")
	_, err = os.Stat(filepath.Join(clone, ".git", "shallow"))
	require.True(t, os.IsNotExist(err), "a plain clone must not be shallow")
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
	_, err = os.Stat(filepath.Join(clone, "Formula", "apptwo.rb"))
	require.NoError(t, err)

	// The smart transfers carried the COMPLETE history (root + child), and
	// the object store is fully connected.
	require.Equal(t, "2", strings.TrimSpace(gitRun(t, clone, "rev-list", "--count", "origin/main")))
	gitRun(t, clone, "fsck")
}

// `git clone --depth 1` through the real router: the deepen handshake works
// end to end (#140's original shallow support, now sourced from the lineage
// history -- the shallow boundary is the tip whose parent the depth cut off).
func TestBrewTapSmart_DepthOneCloneThroughRouter(t *testing.T) {
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	gitTS := gitTapServer(t, env)

	// Grow two commits of history: build once, publish again, redeploy so the
	// next request appends the child commit.
	resp, err := http.Get(gitTS.URL + "/brew/tap.git/info/refs")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	publishBrewProject(t, env, "apptwo", "apptwo-binary")
	server.New(env.cfg, env.database, env.store)

	clone := filepath.Join(t.TempDir(), "tap")
	gitRun(t, t.TempDir(), "clone", "--depth", "1", gitTS.URL+"/brew/tap.git", clone)

	_, err = os.Stat(filepath.Join(clone, ".git", "shallow"))
	require.NoError(t, err, "depth-1 clone of a 2-commit history must be shallow")
	require.Equal(t, "1", strings.TrimSpace(gitRun(t, clone, "rev-list", "--count", "origin/main")))
	_, err = os.Stat(filepath.Join(clone, "Formula", "apptwo.rb"))
	require.NoError(t, err)
}

// The dumb-HTTP path must keep working exactly as #159 shipped it even with
// smart serving registered: a client that never asks for the smart service
// (GIT_SMART_HTTP=0) still clones from the loose-object layout and still
// fast-forwards through Homebrew's update sequence.
func TestBrewTapDumb_StillServesLooseObjectsAndFastForwards(t *testing.T) {
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	gitTS := gitTapServer(t, env)

	dumb := []string{"GIT_SMART_HTTP=0"}
	clone := filepath.Join(t.TempDir(), "tap")
	gitRunEnv(t, t.TempDir(), dumb, "clone", "-c", "fetch.unpackLimit=1", gitTS.URL+"/brew/tap.git", clone)
	// With unpackLimit pinned, a pack transfer would have been KEPT: no pack
	// files proves GIT_SMART_HTTP=0 really used the dumb loose-object path.
	require.False(t, hasPackFiles(t, clone), "a dumb clone fetches loose objects, never a pack")
	tip1 := strings.TrimSpace(gitRun(t, clone, "rev-parse", "origin/main"))

	publishBrewProject(t, env, "apptwo", "apptwo-binary")
	server.New(env.cfg, env.database, env.store)

	gitRunEnv(t, clone, dumb, "fetch", "--force", "origin")
	gitRun(t, clone, "rebase", "origin/main")

	tip2 := strings.TrimSpace(gitRun(t, clone, "rev-parse", "origin/main"))
	require.NotEqual(t, tip1, tip2)
	gitRun(t, clone, "merge-base", "--is-ancestor", tip1, tip2)
	require.Empty(t, strings.TrimSpace(gitRun(t, clone, "status", "--porcelain")))
	_, err := os.Stat(filepath.Join(clone, "Formula", "apptwo.rb"))
	require.NoError(t, err)
}
