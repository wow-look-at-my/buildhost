package auth

import (
	"context"
	"net/http"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type AccessLevel int

const (
	ReadAccess AccessLevel = iota
	WriteAccess
	HiddenReadAccess
)

type RouteInfo interface {
	ProjectName() string
	Access() AccessLevel
}

type ParseFunc func(r *http.Request) RouteInfo

type PublicReadAuthorizer interface {
	AllowsPublicRead(ctx context.Context, database *db.DB, project *db.Project) bool
}

// BuildVisibilityRoute is a read route that names one build: a release or a
// site branch.
type BuildVisibilityRoute interface {
	BuildVisibility(ctx context.Context, database *db.DB, project *db.Project) db.Visibility
}

type readPrivateKey struct{}

// ReadIsPrivate reports whether the build this read route serves is private after every override. A handler uses it, not project.IsPrivate, to pick signed
// links and cache headers.
func ReadIsPrivate(ctx context.Context) bool {
	if v, ok := ctx.Value(readPrivateKey{}).(bool); ok {
		return v
	}
	if p := ProjectFrom(ctx); p != nil {
		return p.IsPrivate
	}
	return false
}
