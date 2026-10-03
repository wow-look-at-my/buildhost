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
