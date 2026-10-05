package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// oidcCtx is the context Authenticate builds for a verified OIDC token.
func oidcCtx(project string, orgTrusted bool) context.Context {
	ctx := WithToken(context.Background(), &db.APIToken{ID: -1, Scopes: "read,write"})
	ctx = WithOIDCProject(ctx, project)
	return WithOIDCOrgTrusted(ctx, orgTrusted)
}

func withVerifierOrgs(t *testing.T, orgs ...string) {
	t.Helper()
	prev := mw
	mw = &Middleware{Verifier: NewOIDCVerifier(OIDCConfig{AllowedOrgs: orgs})}
	if prev != nil {
		mw.DB = prev.DB
	}
	t.Cleanup(func() { mw = prev })
}

func TestOrgNamedIgnoresTheWildcard(t *testing.T) {
	assert.True(t, orgNamed([]string{"wow-look-at-my", "PazerOP"}, "pazerop", ""))
	assert.False(t, orgNamed([]string{"*"}, "pazerop", ""), "the wildcard admits an org but trusts none")
	assert.True(t, orgAllowed([]string{"*"}, "pazerop", ""), "the wildcard still admits provisioning")
	assert.False(t, orgNamed([]string{"PazerOP@42"}, "PazerOP", "7"), "a pinned id must match")
	assert.False(t, orgNamed([]string{"PazerOP"}, "", ""))
}

func TestOIDCOrgTrustedReadsAnotherTrustedRepo(t *testing.T) {
	t.Serial()
	withVerifierOrgs(t, "wow-look-at-my", "PazerOP")

	bashfs := &db.Project{ID: 10, Name: "bashfs", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs", GithubOwnerID: "100"}
	stranger := &db.Project{ID: 11, Name: "stranger", IsPrivate: true, GithubRepo: "someone-else/stranger"}
	noRepo := &db.Project{ID: 12, Name: "admin-made", IsPrivate: true}

	t.Run("a trusted org reads a private project of another trusted org", func(t *testing.T) {
		assert.True(t, TokenCanReadProject(oidcCtx("claude-code-web-config", true), bashfs))
	})
	t.Run("an identity admitted only by the wildcard reads nothing else", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(oidcCtx("claude-code-web-config", false), bashfs))
	})
	t.Run("a project of an untrusted owner stays closed", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(oidcCtx("claude-code-web-config", true), stranger))
	})
	t.Run("a project with no recorded repo stays closed", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(oidcCtx("claude-code-web-config", true), noRepo))
	})
	t.Run("the identity's own namespace still reads", func(t *testing.T) {
		own := &db.Project{ID: 13, Name: "claude-code-web-config/default", IsPrivate: true}
		assert.True(t, TokenCanReadProject(oidcCtx("claude-code-web-config", false), own))
	})
}

func TestOIDCOrgTrustedReadIsPinnedToTheOwnerID(t *testing.T) {
	t.Serial()
	withVerifierOrgs(t, "wow-look-at-my@100")

	pinned := &db.Project{ID: 20, Name: "bashfs", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs", GithubOwnerID: "100"}
	imposter := &db.Project{ID: 21, Name: "bashfs2", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs2", GithubOwnerID: "999"}
	assert.True(t, TokenCanReadProject(oidcCtx("x", true), pinned))
	assert.False(t, TokenCanReadProject(oidcCtx("x", true), imposter))
}

// The read grant must not leak into a write: a publish stays confined to the
// identity's own namespace.
func TestRequireProjectOIDCOrgReadIsReadOnly(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)
	withVerifierOrgs(t, "wow-look-at-my", "PazerOP")

	proj := &db.Project{Name: "bashfs", Versioning: "auto", IsPrivate: true, GithubRepo: "wow-look-at-my/bashfs"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	run := func(access AccessLevel) int {
		handler := requireProjectFunc(func(*http.Request) RouteInfo {
			return testRouteInfo{project: "bashfs", access: access}
		}, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		req := httptest.NewRequest("GET", "/", nil).WithContext(oidcCtx("claude-code-web-config", true))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, run(ReadAccess))
	assert.Equal(t, http.StatusOK, run(HiddenReadAccess))
	assert.Equal(t, http.StatusForbidden, run(WriteAccess))
}
