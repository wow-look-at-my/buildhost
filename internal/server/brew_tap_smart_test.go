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

func gitRunEnv(t *testing.T, dir string, extraEnv []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), gitTestEnv...), extraEnv...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// gitTapServer serves the real router under the git service host so a real
// git client can reach git.{domain} through a plain URL.
func gitTapServer(t *testing.T, env *testEnv) *httptest.Server {
	return hostTapServer(t, env, "git.test.local")
}

// hostTapServer serves the real router under an arbitrary service host.
func hostTapServer(t *testing.T, env *testEnv, host string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = host
		env.handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// hasPackFiles reports whether a clone's object store contains packfiles --
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
func TestBrewTapSmart_GitUpdateFastForwardsAcrossPublishAndRedeploy(t *testing.T) {
	t.Serial()
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	gitTS := gitTapServer(t, env)

	// The router really negotiates smart: this is the response that flips a
	resp, err := http.Get(gitTS.URL + "/brew/tap.git/info/refs?service=git-upload-pack")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/x-git-upload-pack-advertisement", resp.Header.Get("Content-Type"))
	resp.Body.Close()

	clone := filepath.Join(gitScratchDir(t), "tap")
	gitRun(t, gitScratchDir(t), "clone", "-c", "fetch.unpackLimit=1", gitTS.URL+"/brew/tap.git", clone)
	require.True(t, hasPackFiles(t, clone), "a smart clone stores the transferred pack")
	_, err = os.Stat(filepath.Join(clone, ".git", "shallow"))
	require.True(t, os.IsNotExist(err), "a plain clone must not be shallow")
	tip1 := strings.TrimSpace(gitRun(t, clone, "rev-parse", "origin/main"))

	publishBrewProject(t, env, "apptwo", "apptwo-binary")

	// Redeploy: re-wiring over the same data dir must preserve the persisted
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
	require.Equal(t, "2", strings.TrimSpace(gitRun(t, clone, "rev-list", "--count", "origin/main")))
	gitRun(t, clone, "fsck")
}

func TestBrewTapSmart_DepthOneCloneThroughRouter(t *testing.T) {
	t.Serial()
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	gitTS := gitTapServer(t, env)

	resp, err := http.Get(gitTS.URL + "/brew/tap.git/info/refs")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	publishBrewProject(t, env, "apptwo", "apptwo-binary")
	server.New(env.cfg, env.database, env.store)

	clone := filepath.Join(gitScratchDir(t), "tap")
	gitRun(t, gitScratchDir(t), "clone", "--depth", "1", gitTS.URL+"/brew/tap.git", clone)

	_, err = os.Stat(filepath.Join(clone, ".git", "shallow"))
	require.NoError(t, err, "depth-1 clone of a 2-commit history must be shallow")
	require.Equal(t, "1", strings.TrimSpace(gitRun(t, clone, "rev-list", "--count", "origin/main")))
	_, err = os.Stat(filepath.Join(clone, "Formula", "apptwo.rb"))
	require.NoError(t, err)
}

func TestBrewTapSmart_BrewHostTapURLClonesDirectly(t *testing.T) {
	t.Serial()
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	brewTS := hostTapServer(t, env, "brew.test.local")

	resp, err := http.Get(brewTS.URL + "/tap.git/info/refs?service=git-upload-pack")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/x-git-upload-pack-advertisement", resp.Header.Get("Content-Type"))
	resp.Body.Close()

	clone := filepath.Join(gitScratchDir(t), "tap")
	gitRun(t, gitScratchDir(t), "clone", "-c", "fetch.unpackLimit=1", brewTS.URL+"/tap.git", clone)
	require.True(t, hasPackFiles(t, clone), "the brew-host clone must transfer via the smart pack")
	_, err = os.Stat(filepath.Join(clone, ".git", "shallow"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(clone, "Formula", "appone.rb"))
	require.NoError(t, err)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedirect.Get(brewTS.URL + "/tap.git")
	require.NoError(t, err)
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "git.test.local/brew/tap.git")
	resp.Body.Close()
}

func TestBrewTapDumb_StillServesLooseObjectsAndFastForwards(t *testing.T) {
	t.Serial()
	gitOrSkip(t)
	env := setup(t)
	publishBrewProject(t, env, "appone", "appone-binary")
	gitTS := gitTapServer(t, env)

	dumb := []string{"GIT_SMART_HTTP=0"}
	clone := filepath.Join(gitScratchDir(t), "tap")
	gitRunEnv(t, gitScratchDir(t), dumb, "clone", "-c", "fetch.unpackLimit=1", gitTS.URL+"/brew/tap.git", clone)
	// With unpackLimit pinned, a pack transfer would have been KEPT: no pack
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
