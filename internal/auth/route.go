package auth

import "net/http"

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
