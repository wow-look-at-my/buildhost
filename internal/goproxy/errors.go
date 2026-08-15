package goproxy

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind classifies why a module fetch did not produce content. The distinction
// that matters is KindNotFound versus everything else: at the module-proxy
// protocol level a 404/410 means "this module does not exist", and `go mod
// download` reports it as a missing module. Answering it for "I was not allowed
// to read this module" sends every consumer hunting for a typo in go.mod
// instead of at the proxy's credential.
type Kind int

const (
	// KindNotFound: upstream is readable and the module genuinely is not there.
	// The only kind that may answer 404.
	KindNotFound Kind = iota
	// KindUnauthorized: upstream rejected our credential, or we have none. The
	// module may well exist. 403.
	KindUnauthorized
	// KindUpstream: upstream was reachable but failed (5xx, rate limit, malformed
	// response), or was not reachable at all. 502.
	KindUpstream
	// KindInvalidRequest: the request itself is not a well-formed module-proxy
	// request (bad escaping, unsupported module path). 400.
	KindInvalidRequest
)

func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindUnauthorized:
		return "unauthorized"
	case KindUpstream:
		return "upstream"
	case KindInvalidRequest:
		return "invalid_request"
	}
	return "unknown"
}

// Error is a fetch failure carrying enough for a caller to act without reading
// the server's logs: which module, what the upstream actually said, and whether
// the proxy's own credential was the problem.
type Error struct {
	Kind Kind
	// Module and Version address what was being fetched ("" when not applicable).
	Module  string
	Version string
	// Upstream names the system that answered (e.g. "github", "proxy.golang.org")
	// and UpstreamStatus its HTTP status, 0 when the failure was not an HTTP one.
	Upstream       string
	UpstreamStatus int
	// Detail is the upstream's own message, already trimmed to something a
	// terminal can display.
	Detail string
	Err    error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s: module %s", e.Kind, e.Module)
	if e.Version != "" {
		msg += "@" + e.Version
	}
	if e.Upstream != "" {
		msg += fmt.Sprintf(": %s", e.Upstream)
		if e.UpstreamStatus != 0 {
			msg += fmt.Sprintf(" responded %d", e.UpstreamStatus)
		}
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// HTTPStatus is the status this failure is served as. Only a genuine
// KindNotFound is allowed to become a 404.
func (e *Error) HTTPStatus() int {
	switch e.Kind {
	case KindNotFound:
		return http.StatusNotFound
	case KindUnauthorized:
		return http.StatusForbidden
	case KindInvalidRequest:
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

// Body is what the client sees. `go mod download` surfaces a proxy's response
// body verbatim in its error, so this text is the whole diagnosis for whoever
// hits it -- it names the module, the upstream and its status, and for an
// authorization failure it says plainly that the proxy could not read the
// module rather than that the module is absent.
func (e *Error) Body() string {
	s := fmt.Sprintf("buildhost goproxy: %s", e.headline())
	s += fmt.Sprintf("\n  module:   %s", e.Module)
	if e.Version != "" {
		s += fmt.Sprintf("\n  version:  %s", e.Version)
	}
	if e.Upstream != "" {
		s += fmt.Sprintf("\n  upstream: %s", e.Upstream)
		if e.UpstreamStatus != 0 {
			s += fmt.Sprintf(" (HTTP %d)", e.UpstreamStatus)
		}
	}
	if e.Detail != "" {
		s += fmt.Sprintf("\n  detail:   %s", e.Detail)
	}
	if e.Kind == KindUnauthorized {
		s += "\n\nThis is NOT a missing module. The proxy's own upstream credential" +
			"\ncould not read it. Check the proxy's credential and its access to the" +
			"\nrepository -- the admin dashboard's Go proxy page reports both."
	}
	return s + "\n"
}

func (e *Error) headline() string {
	switch e.Kind {
	case KindNotFound:
		return "module not found"
	case KindUnauthorized:
		return "not authorized to read this module"
	case KindInvalidRequest:
		return "invalid module proxy request"
	default:
		return "upstream fetch failed"
	}
}

func notFoundErr(mod, ver, upstream string, status int, detail string) *Error {
	return &Error{Kind: KindNotFound, Module: mod, Version: ver, Upstream: upstream, UpstreamStatus: status, Detail: detail}
}

func unauthorizedErr(mod, ver, upstream string, status int, detail string) *Error {
	return &Error{Kind: KindUnauthorized, Module: mod, Version: ver, Upstream: upstream, UpstreamStatus: status, Detail: detail}
}

func upstreamErr(mod, ver, upstream string, status int, detail string, err error) *Error {
	return &Error{Kind: KindUpstream, Module: mod, Version: ver, Upstream: upstream, UpstreamStatus: status, Detail: detail, Err: err}
}

func invalidErr(mod, ver, detail string) *Error {
	return &Error{Kind: KindInvalidRequest, Module: mod, Version: ver, Detail: detail}
}

// asError normalizes any error into an *Error so a handler never has to guess a
// status. An unclassified error is KindUpstream, never KindNotFound: guessing
// "does not exist" from an error we did not recognize is exactly the laundering
// this type exists to stop.
func asError(mod, ver string, err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Kind: KindUpstream, Module: mod, Version: ver, Detail: err.Error(), Err: err}
}
