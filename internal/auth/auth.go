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
)

// oidcRepo carries the GitHub repo identity from a verified OIDC token, so the
// project-auth middleware can resolve the repo's default branch from GitHub.
type oidcRepo struct {
	repoPath string // "owner/repo"
	issuer   string
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

// WithOIDCRepo records the GitHub repo identity (owner/repo) and issuer from a
// verified OIDC token, so the project-auth middleware can resolve the repo's
// default branch from GitHub.
func WithOIDCRepo(ctx context.Context, repoPath, issuer string) context.Context {
	return context.WithValue(ctx, oidcRepoKey, oidcRepo{repoPath: repoPath, issuer: issuer})
}

// OIDCRepoFrom returns the OIDC repo path ("owner/repo") and issuer, or empty
// strings if none was recorded.
func OIDCRepoFrom(ctx context.Context) (repoPath, issuer string) {
	v, _ := ctx.Value(oidcRepoKey).(oidcRepo)
	return v.repoPath, v.issuer
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
