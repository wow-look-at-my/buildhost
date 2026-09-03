package goproxy

import (
	"errors"
	"fmt"
	"net/http"
)

// Kind classifies why a module fetch did not produce content. The distinction
type Kind int

const (
	// KindNotFound: upstream is readable and the module genuinely is not there.
	KindNotFound Kind = iota
	// KindUnauthorized: upstream rejected our credential, or we have none. The
	KindUnauthorized
	// KindUpstream: upstream was reachable but failed (5xx, rate limit, malformed
	KindUpstream
	// KindInvalidRequest: the request itself is not a well-formed module-proxy
	KindInvalidRequest
	KindInaccessible
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
	case KindInaccessible:
		return "inaccessible"
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
	// Upstream names the system that answered (e.g. "github", a mirror's URL)
	Upstream       string
	UpstreamStatus int
	// Detail is the upstream's own message, already trimmed to something a
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
func (e *Error) HTTPStatus() int {
	switch e.Kind {
	case KindNotFound, KindInaccessible:
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
	case KindNotFound, KindInaccessible:
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

// inaccessibleErr answers a caller who may not see this module.
//
// The answer is identical whether the module exists or not: the check runs
// before any upstream call, so there is nothing that could differ. That is what
// keeps a prober from mapping the org's private repositories by diffing
// responses. The note is on both answers too, so it tells a caller who simply
func inaccessibleErr(mod, ver string) *Error {
	return &Error{
		Kind:    KindInaccessible,
		Module:  mod,
		Version: ver,
		Detail: "no such module is visible to this caller. A module in one of this proxy's " +
			"private namespaces is served only to an authenticated caller: send a read-scoped " +
			"buildhost token (Authorization: Bearer, HTTP Basic password, or ?token=). A module " +
			"that does not exist is answered exactly this way.",
	}
}

// notServedErr answers a module outside this proxy's namespace, with no mirror
// configured to forward it to.
func notServedErr(mod, ver string, servedPrefixes []string) *Error {
	return &Error{
		Kind:    KindNotFound,
		Module:  mod,
		Version: ver,
		Detail: "not served by this proxy (it serves " + joinOr(servedPrefixes, "no module prefixes") +
			", and no upstream mirror is configured). Use GOPROXY=<this proxy>,direct so the go " +
			"command fetches everything else straight from its origin.",
	}
}

func joinOr(items []string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	out := items[0]
	for _, s := range items[1:] {
		out += ", " + s
	}
	return out
}

// asError normalizes any error into an *Error so a handler never has to guess a
// status. An unclassified error is KindUpstream, never KindNotFound: guessing
func asError(mod, ver string, err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Kind: KindUpstream, Module: mod, Version: ver, Detail: err.Error(), Err: err}
}
