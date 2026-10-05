package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// actorCtx is the context Authenticate builds for a verified GitHub Actions token.
func actorCtx(project, actor string) context.Context {
	ctx := WithToken(context.Background(), &db.APIToken{ID: -1, Scopes: "read,write"})
	ctx = WithOIDCProject(ctx, project)
	return WithOIDCRepo(ctx, OIDCRepoIdentity{
		RepoPath: "PazerOP/" + project,
		Issuer:   GitHubActionsIssuer,
		Actor:    actor,
	})
}

// withStubPermissions serves the collaborator permission endpoint from perms,
// keyed "owner/repo/login".
func withStubPermissions(t *testing.T, perms map[string]string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	withStubGitHub(t, "pat", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer pat", r.Header.Get("Authorization"))
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 6 || parts[0] != "repos" || parts[3] != "collaborators" || parts[5] != "permission" {
			http.NotFound(w, r)
			return
		}
		perm, ok := perms[parts[1]+"/"+parts[2]+"/"+parts[4]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]string{"permission": perm}))
	})
	t.Cleanup(func() {
		actorReadMu.Lock()
		actorReadCache = map[string]actorReadEntry{}
		actorReadMu.Unlock()
	})
	return &calls
}

func TestOIDCActorReadsAnotherRepo(t *testing.T) {
	t.Serial()
	withStubPermissions(t, map[string]string{
		"wow-look-at-my/bashfs/PazerOP": "admin",
		"wow-look-at-my/bashfs/reader":  "read",
		"wow-look-at-my/bashfs/nobody":  "none",
	})

	bashfs := &db.Project{ID: 10, Name: "bashfs", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs"}
	noRepo := &db.Project{ID: 12, Name: "admin-made", IsPrivate: true}

	t.Run("an actor with access reads the project", func(t *testing.T) {
		assert.True(t, TokenCanReadProject(actorCtx("claude-code-web-config", "PazerOP"), bashfs))
		assert.True(t, TokenCanReadProject(actorCtx("claude-code-web-config", "reader"), bashfs))
	})
	t.Run("an actor GitHub reports with no access is refused", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(actorCtx("claude-code-web-config", "nobody"), bashfs))
		assert.False(t, TokenCanReadProject(actorCtx("claude-code-web-config", "stranger"), bashfs))
	})
	t.Run("a token with no actor is refused", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(actorCtx("claude-code-web-config", ""), bashfs))
	})
	t.Run("a project with no recorded repo stays closed", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(actorCtx("claude-code-web-config", "PazerOP"), noRepo))
	})
	t.Run("the identity's own namespace reads without GitHub", func(t *testing.T) {
		own := &db.Project{ID: 13, Name: "claude-code-web-config/default", IsPrivate: true}
		assert.True(t, TokenCanReadProject(actorCtx("claude-code-web-config", ""), own))
	})
}

func TestOIDCActorReadNeedsTheGitHubIssuer(t *testing.T) {
	t.Serial()
	calls := withStubPermissions(t, map[string]string{"wow-look-at-my/bashfs/PazerOP": "admin"})

	ctx := WithToken(context.Background(), &db.APIToken{ID: -1, Scopes: "read,write"})
	ctx = WithOIDCProject(ctx, "x")
	ctx = WithOIDCRepo(ctx, OIDCRepoIdentity{Issuer: "https://other.example", Actor: "PazerOP"})
	bashfs := &db.Project{ID: 10, Name: "bashfs", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs"}
	assert.False(t, TokenCanReadProject(ctx, bashfs))
	assert.Zero(t, calls.Load(), "another issuer's actor claim must not reach GitHub")
}

func TestOIDCActorReadCachesAndDeniesOnFailure(t *testing.T) {
	t.Serial()
	calls := withStubPermissions(t, map[string]string{"wow-look-at-my/bashfs/PazerOP": "write"})
	bashfs := &db.Project{ID: 10, Name: "bashfs", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs"}

	assert.True(t, TokenCanReadProject(actorCtx("x", "PazerOP"), bashfs))
	assert.True(t, TokenCanReadProject(actorCtx("x", "PazerOP"), bashfs))
	assert.Equal(t, int32(1), calls.Load())

	withStubGitHub(t, "pat", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	other := &db.Project{ID: 11, Name: "other", IsPrivate: true, GithubRepo: "wow-look-at-my/other"}
	assert.False(t, TokenCanReadProject(actorCtx("x", "PazerOP"), other), "a failed lookup denies")
	actorReadMu.Lock()
	_, cached := actorReadCache["pazerop\x00wow-look-at-my/other"]
	actorReadMu.Unlock()
	assert.False(t, cached, "a failed lookup is not cached")
}

// The read grant must not leak into a write: a publish stays confined to the
// identity's own namespace.
func TestRequireProjectOIDCActorReadIsReadOnly(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)
	withStubPermissions(t, map[string]string{"wow-look-at-my/bashfs/PazerOP": "admin"})

	proj := &db.Project{Name: "bashfs", Versioning: "auto", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	run := func(access AccessLevel) int {
		handler := requireProjectFunc(func(*http.Request) RouteInfo {
			return testRouteInfo{project: "bashfs", access: access}
		}, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		req := httptest.NewRequest("GET", "/", nil).WithContext(actorCtx("claude-code-web-config", "PazerOP"))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, run(ReadAccess))
	assert.Equal(t, http.StatusOK, run(HiddenReadAccess))
	assert.Equal(t, http.StatusForbidden, run(WriteAccess))
}
