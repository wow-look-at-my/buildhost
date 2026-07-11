package brew

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// tapGitServer serves the handler's tap over real HTTP with a FIXED host, so a
// real git client can clone it and the lineage key stays stable across handler
// instances (httptest picks a fresh port per instance; the Host header is what
// the lineage keys by).
func tapGitServer(t *testing.T, h *Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "git.tap.test"
		h.ServeTap(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func runGit(t *testing.T, dir string, args ...string) string {
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

// THE regression test for the tap history rewrite. Homebrew's updater runs
// `git fetch --force` + `git rebase origin/main` per tap on every
// `brew update`. Before tap history was persisted, every publish minted an
// unrelated PARENTLESS root commit, and that sequence replayed the client's
// old root onto the new one: add/add conflicts, every client wedged mid-rebase
// with conflict markers. The exact sequence must instead fast-forward, with
// the new tip a descendant of the old.
func TestTap_BrewUpdateFastForwardsAcrossPublishes(t *testing.T) {
	requireGit(t)
	oldTTL := tapCacheTTL
	tapCacheTTL = 0 // re-check content on every request
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	ts := tapGitServer(t, h)
	clone := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", ts.URL+"/brew/tap.git", clone)
	oldTip := strings.TrimSpace(runGit(t, clone, "rev-parse", "origin/main"))

	// A publish changes the tap contents...
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")

	// ...and the client's next update -- exactly Homebrew's sequence -- must
	// fast-forward with zero conflicts.
	runGit(t, clone, "fetch", "--force", "origin")
	runGit(t, clone, "rebase", "origin/main")

	newTip := strings.TrimSpace(runGit(t, clone, "rev-parse", "origin/main"))
	require.NotEqual(t, oldTip, newTip)
	runGit(t, clone, "merge-base", "--is-ancestor", oldTip, newTip)

	// The rebase landed cleanly at the new tip: no in-progress rebase state,
	// no conflict markers, and the new formula checked out.
	assert.Equal(t, newTip, strings.TrimSpace(runGit(t, clone, "rev-parse", "HEAD")))
	assert.Empty(t, strings.TrimSpace(runGit(t, clone, "status", "--porcelain")))
	_, err := os.Stat(filepath.Join(clone, "Formula", "apptwo.rb"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(clone, "Formula", "appone.rb"))
	require.NoError(t, err)
}

// A restart (or redeploy) re-wires the handler the way OnReady does --
// including resetTapCache, which must NOT wipe the persisted history. The tip
// survives byte-identically while content is unchanged, and a publish after
// the restart still fast-forwards a clone taken before it.
func TestTap_RestartPreservesHistoryAndFastForwards(t *testing.T) {
	requireGit(t)
	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	ts := tapGitServer(t, h)
	clone := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", ts.URL+"/brew/tap.git", clone)
	tip1 := strings.TrimSpace(runGit(t, clone, "rev-parse", "origin/main"))

	// "Restart": a fresh handler instance over the same DataDir, wired the way
	// OnReady wires it (resetTapCache included).
	h2 := &Handler{DB: d, Store: store, Gen: h.Gen, TmpDir: h.TmpDir, DataDir: h.DataDir}
	h2.resetTapCache()
	ts2 := tapGitServer(t, h2)

	resp, err := http.Get(ts2.URL + "/brew/tap.git/info/refs")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, tip1+"\trefs/heads/main\n", string(body),
		"unchanged content must keep the exact tip across a restart")

	// A publish after the restart appends to the SAME history.
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")
	runGit(t, clone, "remote", "set-url", "origin", ts2.URL+"/brew/tap.git")
	runGit(t, clone, "fetch", "--force", "origin")
	runGit(t, clone, "rebase", "origin/main")

	tip2 := strings.TrimSpace(runGit(t, clone, "rev-parse", "origin/main"))
	require.NotEqual(t, tip1, tip2)
	runGit(t, clone, "merge-base", "--is-ancestor", tip1, tip2)
	assert.Empty(t, strings.TrimSpace(runGit(t, clone, "status", "--porcelain")))
}

// The persisted lineage store is capped: junk Host headers (each one is a new
// lineage) must not grow {DataDir}/brew-tap without bound. Eviction is
// whole-lineage, LRU by directory mtime, and the lineage being created always
// survives its own build.
func TestTap_LineageCapEvictsLRU(t *testing.T) {
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	lastHost := ""
	for i := 0; i < tapHistoryMaxLineages+8; i++ {
		lastHost = fmt.Sprintf("git.h%03d.test", i)
		rec := getTap(t, h, lastHost, "info/refs")
		require.Equal(t, http.StatusOK, rec.Code)
	}

	entries, err := os.ReadDir(h.tapHistoryRoot())
	require.NoError(t, err)
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	assert.LessOrEqual(t, dirs, tapHistoryMaxLineages)

	// The most recently built lineage is present (evictions strike before the
	// new lineage is created, never at it).
	lastKey := "https://" + strings.TrimPrefix(lastHost, "git.") + "\x00anon"
	_, err = os.Stat(h.tapLineageDir(lastKey))
	assert.NoError(t, err)
}
