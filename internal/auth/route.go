package auth

import "net/http"

type AccessLevel int

const (
	ReadAccess  AccessLevel = iota
	WriteAccess
)

type RouteInfo interface {
	ProjectName() string
	Access() AccessLevel
}

type ParseFunc func(r *http.Request) RouteInfo
