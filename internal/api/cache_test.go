package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// cacheRepo returns testRunRepo as a run of ref.
func cacheRepo(ref string) auth.OIDCRepoIdentity {
	repo := testRunRepo
	repo.Ref = ref
	return repo
}

// withDefaultBranch makes the repository's default branch answer branch, or
// nothing when branch is "".
func withDefaultBranch(t *testing.T, branch string) {
	t.Helper()
	prev := githubDefaultBranch
	githubDefaultBranch = func(context.Context, string) string { return branch }
	t.Cleanup(func() { githubDefaultBranch = prev })
}

func putCache(t *testing.T, h *Handler, repo auth.OIDCRepoIdentity, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"repository": {repo.RepoPath}}
	req := httptest.NewRequest("PUT", "/api/v1/cache/"+key+"?"+q.Encode(), bytes.NewReader(body))
	req.SetPathValue("key", key)
	req = req.WithContext(runLockCtx(req.Context(), repo))
	rec := httptest.NewRecorder()
	h.PutCache(rec, req)
	return rec
}

func findCache(t *testing.T, h *Handler, repo auth.OIDCRepoIdentity, key string, restoreKeys ...string) (*httptest.ResponseRecorder, cacheFindResponse) {
	t.Helper()
	q := url.Values{"repository": {repo.RepoPath}, "key": {key}, "restore_keys": restoreKeys}
	req := httptest.NewRequest("GET", "/api/v1/cache?"+q.Encode(), nil)
	req = req.WithContext(runLockCtx(req.Context(), repo))
	rec := httptest.NewRecorder()
	h.FindCache(rec, req)
	var resp cacheFindResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	return rec, resp
}

func getCache(t *testing.T, h *Handler, repo auth.OIDCRepoIdentity, key, ref string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"repository": {repo.RepoPath}, "ref": {ref}}
	req := httptest.NewRequest("GET", "/api/v1/cache/"+key+"?"+q.Encode(), nil)
	req.SetPathValue("key", key)
	req = req.WithContext(runLockCtx(req.Context(), repo))
	rec := httptest.NewRecorder()
	h.GetCache(rec, req)
	return rec
}

func TestCache_SaveThenRestoreExact(t *testing.T) {
	t.Serial()
	withDefaultBranch(t, "master")
	h := setupTestHandler(t)
	master := cacheRepo("refs/heads/master")
	payload := bytes.Repeat([]byte("go-build "), 4096)

	rec := putCache(t, h, master, "dist-bootstrap-Linux-abc", payload)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rec, found := findCache(t, h, master, "dist-bootstrap-Linux-abc")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, found.cacheEntryResponse)
	assert.True(t, found.Exact)
	assert.Equal(t, "refs/heads/master", found.Ref)
	assert.Equal(t, []string{"refs/heads/master"}, found.Scopes, "the default branch is the caller's ref, so it is searched once")

	rec = getCache(t, h, master, "dist-bootstrap-Linux-abc", found.Ref)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, payload, rec.Body.Bytes())
	assert.Equal(t, found.SHA256, rec.Header().Get("X-Content-SHA256"))
}

func TestCache_RestoreKeysTakeTheNewestPrefixMatch(t *testing.T) {
	t.Serial()
	withDefaultBranch(t, "master")
	h := setupTestHandler(t)
	master := cacheRepo("refs/heads/master")
	require.Equal(t, http.StatusCreated, putCache(t, h, master, "wasm-old", []byte("old")).Code)
	_, err := h.DB.Exec(`UPDATE cache_entries SET created_at = datetime('now', '-1 hour') WHERE key = 'wasm-old'`)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, putCache(t, h, master, "wasm-new", []byte("new")).Code)

	rec, found := findCache(t, h, master, "wasm-missing", "nothing-", "wasm-")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "wasm-new", found.Key, "the newest entry the prefix matches wins")
	assert.False(t, found.Exact)
}

func TestCache_ABranchReadsItsOwnRefThenTheDefaultBranch(t *testing.T) {
	t.Serial()
	withDefaultBranch(t, "master")
	h := setupTestHandler(t)
	master := cacheRepo("refs/heads/master")
	pr := cacheRepo("refs/pull/7/merge")
	other := cacheRepo("refs/pull/8/merge")
	require.Equal(t, http.StatusCreated, putCache(t, h, master, "deps-1", []byte("master")).Code)
	require.Equal(t, http.StatusCreated, putCache(t, h, other, "deps-2", []byte("other pull request")).Code)

	rec, found := findCache(t, h, pr, "deps-3", "deps-")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "deps-1", found.Key, "a pull request reads the default branch, never another pull request")
	assert.Equal(t, "refs/heads/master", found.Ref)
	assert.Equal(t, []string{"refs/pull/7/merge", "refs/heads/master"}, found.Scopes)

	require.Equal(t, http.StatusCreated, putCache(t, h, pr, "deps-4", []byte("own")).Code)
	_, found = findCache(t, h, pr, "deps-5", "deps-")
	assert.Equal(t, "deps-4", found.Key, "the run's own ref comes before the default branch")

	rec = getCache(t, h, pr, "deps-2", "refs/pull/8/merge")
	assert.Equal(t, http.StatusForbidden, rec.Code, "another pull request's entries are not readable")
	rec = getCache(t, h, master, "deps-4", "refs/pull/7/merge")
	assert.Equal(t, http.StatusForbidden, rec.Code, "the default branch does not read a pull request's entries")
}

func TestCache_AnUnknownDefaultBranchIsNamed(t *testing.T) {
	t.Serial()
	withDefaultBranch(t, "")
	h := setupTestHandler(t)
	rec, found := findCache(t, h, cacheRepo("refs/pull/7/merge"), "deps-1")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, []string{"refs/pull/7/merge"}, found.Scopes)
	assert.Contains(t, found.DefaultBranch, "GitHub did not answer")
}

func TestCache_AKeyIsWrittenOnce(t *testing.T) {
	t.Serial()
	withDefaultBranch(t, "master")
	h := setupTestHandler(t)
	master := cacheRepo("refs/heads/master")
	require.Equal(t, http.StatusCreated, putCache(t, h, master, "k", []byte("first")).Code)
	rec := putCache(t, h, master, "k", []byte("second"))
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	rec = getCache(t, h, master, "k", "refs/heads/master")
	assert.Equal(t, "first", rec.Body.String(), "the second save stores nothing")
}

func TestCache_RefusesAMismatchedOrIncompleteCaller(t *testing.T) {
	t.Serial()
	withDefaultBranch(t, "master")
	h := setupTestHandler(t)
	noRef := cacheRepo("")
	assert.Equal(t, http.StatusForbidden, putCache(t, h, noRef, "k", []byte("x")).Code, "a token with no ref names no scope")

	master := cacheRepo("refs/heads/master")
	q := url.Values{"repository": {"someone/else"}, "key": {"k"}}
	req := httptest.NewRequest("GET", "/api/v1/cache?"+q.Encode(), nil)
	req = req.WithContext(runLockCtx(req.Context(), master))
	rec := httptest.NewRecorder()
	h.FindCache(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the request names another repository than its token")

	assert.Equal(t, http.StatusBadRequest, putCache(t, h, master, "bad%20key", []byte("x")).Code)
}
