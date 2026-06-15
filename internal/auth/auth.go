package auth

import (
	"context"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type contextKey int

const (
	tokenKey contextKey = iota
	projectKey
	routeKey
	oidcProjectKey
	oidcPrivateKey
	oidcErrorKey
	oidcRepoKey
	userKey
	githubTokenKey
)

// WithOIDCRepo records the full GitHub owner/repo from an OIDC publish subject,
// so requireProject can persist it on the project (for later GitHub-login authz).
func WithOIDCRepo(ctx context.Context, repo string) context.Context {
	return context.WithValue(ctx, oidcRepoKey, repo)
}

func OIDCRepoFrom(ctx context.Context) string {
	s, _ := ctx.Value(oidcRepoKey).(string)
	return s
}

// WithGitHubToken stashes the signed-in user's GitHub OAuth token (from the
// session) so requireProject can check their access to a project's repo.
func WithGitHubToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, githubTokenKey, token)
}

func GitHubTokenFrom(ctx context.Context) string {
	s, _ := ctx.Value(githubTokenKey).(string)
	return s
}

// WithUser marks the request as a signed-in human (identity is their GitHub
// login), set from a verified bh_session cookie after a Sign in with GitHub
// flow. A request carrying it is allowed to read private resources -- membership
// of an allowed org was checked at login. It never grants write.
func WithUser(ctx context.Context, login string) context.Context {
	return context.WithValue(ctx, userKey, login)
}

// UserFrom returns the signed-in GitHub login and whether the request is
// authenticated as a human.
func UserFrom(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(userKey).(string)
	return s, ok && s != ""
}

func WithToken(ctx context.Context, t *db.APIToken) context.Context {
	return context.WithValue(ctx, tokenKey, t)
}

func TokenFrom(ctx context.Context) *db.APIToken {
	t, _ := ctx.Value(tokenKey).(*db.APIToken)
	return t
}

func WithProject(ctx context.Context, p *db.Project) context.Context {
	return context.WithValue(ctx, projectKey, p)
}

func ProjectFrom(ctx context.Context) *db.Project {
	p, _ := ctx.Value(projectKey).(*db.Project)
	return p
}

func WithRouteInfo(ctx context.Context, ri RouteInfo) context.Context {
	return context.WithValue(ctx, routeKey, ri)
}

func RouteInfoFrom(ctx context.Context) RouteInfo {
	ri, _ := ctx.Value(routeKey).(RouteInfo)
	return ri
}

func WithOIDCProject(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, oidcProjectKey, name)
}

func OIDCProjectFrom(ctx context.Context) string {
	s, _ := ctx.Value(oidcProjectKey).(string)
	return s
}

func WithOIDCPrivate(ctx context.Context, private bool) context.Context {
	return context.WithValue(ctx, oidcPrivateKey, private)
}

func OIDCPrivateFrom(ctx context.Context) (bool, bool) {
	v, ok := ctx.Value(oidcPrivateKey).(bool)
	return v, ok
}

// WithOIDCError records why OIDC verification failed for a presented JWT, so an
// eventual 401 can explain the reason instead of a bare "authentication
// required". It is set only when a JWT was presented and rejected.
func WithOIDCError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, oidcErrorKey, err)
}

// OIDCErrorFrom returns the recorded OIDC verification failure, or nil.
func OIDCErrorFrom(ctx context.Context) error {
	err, _ := ctx.Value(oidcErrorKey).(error)
	return err
}
