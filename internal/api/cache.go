// A cache entry is a directory a CI job saves under a key, for jobs of later runs to restore.

package api

import (
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
)

// A cache entry is a directory a CI job saves under a key, for jobs of later
// runs to restore. It is not a release. The cache janitor evicts an entry
// nothing restored for a while, and the least recently used when disk is low.
func init() {
	auth.HandleRawPrimary("GET /api/v1/cache", handler.FindCache)
	auth.HandleRawPrimary("PUT /api/v1/cache/{key}", handler.PutCache)
	auth.HandleRawPrimary("GET /api/v1/cache/{key}", handler.GetCache)
}

var validCacheKey = regexp.MustCompile(`^[A-Za-z0-9._-]{1,512}$`)

// githubDefaultBranch answers a repository's default branch. Tests replace it.
var githubDefaultBranch = auth.GitHubDefaultBranch

const cacheKeyRule = "1-512 of [A-Za-z0-9._-]"

// cacheFallbackBranch is the default branch a restore reads when GitHub does not answer it.
const cacheFallbackBranch = "master"

type cacheEntryResponse struct {
	Key    string `json:"key"`
	Ref    string `json:"ref"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Exact  bool   `json:"exact"`
}

// cacheFindResponse is a restore's answer. Scopes are the refs it searched, in
// order. DefaultBranch says why the default branch was not among them.
type cacheFindResponse struct {
	Key           string   `json:"key,omitempty"`
	Ref           string   `json:"ref,omitempty"`
	Size          int64    `json:"size,omitempty"`
	SHA256        string   `json:"sha256,omitempty"`
	Exact         bool     `json:"exact,omitempty"`
	Scopes        []string `json:"scopes"`
	DefaultBranch string   `json:"default_branch,omitempty"`
}

// cacheCaller returns the repository and ref the request's OIDC token names.
// The request also names its repository, and a request that names another is
// refused. It writes the error and returns false.
func cacheCaller(w http.ResponseWriter, r *http.Request) (auth.OIDCRepoIdentity, bool) {
	repo, ok := auth.RunRepoFrom(r.Context())
	if !ok && auth.TokenFrom(r.Context()) == nil {
		jsonError(w, http.StatusUnauthorized, "authentication required: present the job's GitHub Actions OIDC token")
		return repo, false
	}
	if !ok || repo.Ref == "" {
		jsonError(w, http.StatusForbidden, "the cache needs a GitHub Actions OIDC token that names its repository id and ref")
		return repo, false
	}
	if got := r.URL.Query().Get("repository"); !strings.EqualFold(got, repo.RepoPath) {
		jsonError(w, http.StatusForbidden, "the request names repository "+got+", but the token was minted by "+repo.RepoPath)
		return repo, false
	}
	return repo, true
}

// cacheScopes returns the scopes a restore reads: the caller's ref, then the
// default branch. note says why the default branch is absent, or that it is
// master because GitHub did not answer.
func cacheScopes(r *http.Request, repo auth.OIDCRepoIdentity) (scopes []db.CacheScope, note string) {
	scopes = []db.CacheScope{{RepoID: repo.RepoID, Ref: repo.Ref}}
	if repo.Issuer != auth.GitHubActionsIssuer {
		return scopes, "not read: the token is not from GitHub Actions"
	}
	branch := githubDefaultBranch(r.Context(), repo.RepoPath)
	if branch == "" {
		slog.WarnContext(r.Context(), "cache: GitHub does not answer the default branch; the restore reads "+cacheFallbackBranch, "repo", repo.RepoPath)
		branch = cacheFallbackBranch
		note = cacheFallbackBranch + ": GitHub did not answer the repository's default branch"
	}
	if ref := "refs/heads/" + branch; ref != repo.Ref {
		scopes = append(scopes, db.CacheScope{RepoID: repo.RepoID, Ref: ref})
	}
	return scopes, note
}

// FindCache answers the entry a restore reads for key and restore_keys, as
// GitHub's cache does. This covers the exact key, then each restore key as
// a prefix.
func (h *Handler) FindCache(w http.ResponseWriter, r *http.Request) {
	repo, ok := cacheCaller(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	key := q.Get("key")
	if !validCacheKey.MatchString(key) {
		jsonError(w, http.StatusBadRequest, "invalid cache key: want "+cacheKeyRule)
		return
	}
	restoreKeys := q["restore_keys"]
	for _, prefix := range restoreKeys {
		if !validCacheKey.MatchString(prefix) {
			jsonError(w, http.StatusBadRequest, "invalid restore key "+strconv.Quote(prefix)+": want "+cacheKeyRule)
			return
		}
	}
	scopes, note := cacheScopes(r, repo)
	refs := make([]string, 0, len(scopes))
	for _, s := range scopes {
		refs = append(refs, s.Ref)
	}
	match, found, err := h.DB.FindCache(r.Context(), scopes, key, restoreKeys)
	if err != nil {
		slog.ErrorContext(r.Context(), "cache: find", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to read the cache")
		return
	}
	resp := cacheFindResponse{Scopes: refs, DefaultBranch: note}
	if !found {
		jsonResponse(w, http.StatusNotFound, resp)
		return
	}
	resp.Key, resp.Ref, resp.Size, resp.SHA256, resp.Exact = match.Key, match.Scope.Ref, match.Size, match.StorageKey, match.Key == key
	jsonResponse(w, http.StatusOK, resp)
}

// PutCache stores the body under key in the caller's ref.
func (h *Handler) PutCache(w http.ResponseWriter, r *http.Request) {
	repo, ok := cacheCaller(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !validCacheKey.MatchString(key) {
		jsonError(w, http.StatusBadRequest, "invalid cache key: want "+cacheKeyRule)
		return
	}
	ctx := r.Context()
	scope := db.CacheScope{RepoID: repo.RepoID, Ref: repo.Ref}
	if _, held, err := h.DB.GetCache(ctx, scope, key); err != nil {
		slog.ErrorContext(ctx, "cache: read", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to read the cache")
		return
	} else if held {
		jsonError(w, http.StatusConflict, repo.Ref+" already holds cache key "+key)
		return
	}

	storageKey, size, ok := h.storeCIBody(w, r, "cache entry")
	if !ok {
		return
	}

	inserted, err := h.DB.PutCache(ctx, scope, key, repo.RepoPath, db.RunBlob{StorageKey: storageKey, Size: size})
	if err != nil {
		slog.ErrorContext(ctx, "cache: record", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to record cache entry")
		return
	}
	if !inserted {
		// A concurrent save of the same key won. This body is not kept.
		if _, err := retention.DeleteBlobIfUnreferenced(ctx, h.DB, h.Store, storageKey, true); err != nil {
			slog.ErrorContext(ctx, "cache: delete the losing blob", "key", storageKey, "err", err)
		}
		jsonError(w, http.StatusConflict, repo.Ref+" already holds cache key "+key)
		return
	}
	slog.InfoContext(ctx, "cache entry stored", "repo", repo.RepoPath, "ref", repo.Ref, "key", key, "size", size)
	jsonResponse(w, http.StatusCreated, cacheEntryResponse{Key: key, Ref: repo.Ref, Size: size, SHA256: storageKey, Exact: true})
}

// GetCache serves the entry key in scope ref, which must be one a restore of
// the caller reads. It honors Range, so a download that breaks can resume.
func (h *Handler) GetCache(w http.ResponseWriter, r *http.Request) {
	repo, ok := cacheCaller(w, r)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !validCacheKey.MatchString(key) {
		jsonError(w, http.StatusBadRequest, "invalid cache key: want "+cacheKeyRule)
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = repo.Ref
	}
	ctx := r.Context()
	scopes, _ := cacheScopes(r, repo)
	var scope db.CacheScope
	for _, s := range scopes {
		if s.Ref == ref {
			scope = s
		}
	}
	if scope.Ref == "" {
		jsonError(w, http.StatusForbidden, "a run of "+repo.Ref+" reads the cache of its own ref and of the default branch, not of "+ref)
		return
	}
	entry, found, err := h.DB.GetCache(ctx, scope, key)
	if err != nil {
		slog.ErrorContext(ctx, "cache: read", "err", err)
		jsonError(w, http.StatusInternalServerError, "failed to read the cache")
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, ref+" holds no cache key "+key)
		return
	}
	// A resumed download asks again with Range. Only the first request of a
	// download counts as a use.
	if r.Header.Get("Range") == "" {
		if err := h.DB.MarkCacheRead(ctx, scope, key); err != nil {
			slog.ErrorContext(ctx, "cache: count read", "err", err)
		}
	}

	h.serveCIBlob(w, r, key, entry.StorageKey, "cache entry")
}
