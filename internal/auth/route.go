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
	// HiddenReadAccess is read access for an endpoint that must not reveal the
	HiddenReadAccess
)

type RouteInfo interface {
	ProjectName() string
	Access() AccessLevel
}

type ParseFunc func(r *http.Request) RouteInfo

// PublicReadAuthorizer is an optional capability a RouteInfo may implement to
type PublicReadAuthorizer interface {
	AllowsPublicRead(ctx context.Context, database *db.DB, project *db.Project) bool
}
