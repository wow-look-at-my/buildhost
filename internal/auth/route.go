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
	// existence of a project the caller may not see. requireProject treats it
	// like ReadAccess, except a private project the caller is not authorized for
	// (and a project that does not exist) both yield 404 -- never 401/403 -- and
	// it never auto-provisions a missing project. Used by the public web
	// frontend so private projects 404 like GitHub instead of leaking via 401.
	HiddenReadAccess
)

type RouteInfo interface {
	ProjectName() string
	Access() AccessLevel
}

type ParseFunc func(r *http.Request) RouteInfo

// PublicReadAuthorizer is an optional capability a RouteInfo may implement to
// declare that the specific resource a read addresses is publicly readable even
// when its project is private -- e.g. a static site explicitly published as
// public. requireProject consults it before gating a read on a private project,
// so the authorization decision stays centralized (handlers never check auth)
// while the route supplies the per-resource fact. It is consulted only for
// ReadAccess on a private project; a true result serves the read without a token.
type PublicReadAuthorizer interface {
	AllowsPublicRead(ctx context.Context, database *db.DB, project *db.Project) bool
}
