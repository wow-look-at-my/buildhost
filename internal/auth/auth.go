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
	sessionTokenDeadKey
	runLockRepoKey
)

// OIDCRepoIdentity carries the GitHub repo identity from a verified OIDC
type OIDCRepoIdentity struct {
	RepoPath string // "owner/repo" (plain names, IDs stripped)
	Issuer   string
	OwnerID  string
	RepoID   string
	// RunID / RunAttempt name the workflow run and attempt that minted the token.
	RunID      string
	RunAttempt string
	// Actor is the GitHub login that triggered the run.
	Actor string
}

func WithGitHubToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, githubTokenKey, token)
}

func GitHubTokenFrom(ctx context.Context) string {
	s, _ := ctx.Value(githubTokenKey).(string)
	return s
}

// WithSessionTokenDead marks the request's bh_session as carrying a dead GitHub
func WithSessionTokenDead(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionTokenDeadKey, true)
}

// SessionTokenDeadFrom reports whether the session's GitHub token was found
// dead by the repo-access probe.
func SessionTokenDeadFrom(ctx context.Context) bool {
	v, _ := ctx.Value(sessionTokenDeadKey).(bool)
	return v
}

// WithUser marks the request as a signed-in human (identity is their GitHub
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

// WithOIDCRepo records the GitHub repo identity (owner/repo, issuer, numeric
func WithOIDCRepo(ctx context.Context, identity OIDCRepoIdentity) context.Context {
	return context.WithValue(ctx, oidcRepoKey, identity)
}

func OIDCRepoFrom(ctx context.Context) OIDCRepoIdentity {
	v, _ := ctx.Value(oidcRepoKey).(OIDCRepoIdentity)
	return v
}

// withRunLockOnlyRepo records the identity of a verified token that the event
// allowlist refused. Only RunLockRepoFrom reads it.
func withRunLockOnlyRepo(ctx context.Context, identity OIDCRepoIdentity) context.Context {
	return context.WithValue(ctx, runLockRepoKey, identity)
}

// RunLockRepoFrom returns the run identity a run lock request may act for: the
// identity of an accepted OIDC token, or of a verified token that only the
// event allowlist refused. A run lock grants nothing but the run's own locks,
// so a scheduled run can use them while the rest of the API refuses it.
func RunLockRepoFrom(ctx context.Context) (OIDCRepoIdentity, bool) {
	if t := TokenFrom(ctx); t != nil && t.HasScope("write") {
		if repo := OIDCRepoFrom(ctx); repo.RepoID != "" {
			return repo, true
		}
	}
	repo, ok := ctx.Value(runLockRepoKey).(OIDCRepoIdentity)
	return repo, ok && repo.RepoID != ""
}


// WithOIDCError records why OIDC verification failed for a presented JWT.
func WithOIDCError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, oidcErrorKey, err)
}

// OIDCErrorFrom returns the recorded OIDC verification failure, or nil.
func OIDCErrorFrom(ctx context.Context) error {
	err, _ := ctx.Value(oidcErrorKey).(error)
	return err
}
