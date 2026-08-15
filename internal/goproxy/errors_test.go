package goproxy

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The invariant this whole package turns on: only a genuine "upstream is
// readable and the module is absent" may leave as a 404. `go mod download`
// reports a 404 as a missing module, so any other failure wearing that status
// costs every consumer the same misdiagnosis.
func TestOnlyNotFoundBecomes404(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		want int
	}{
		{"genuinely absent", notFoundErr("m", "v1.0.0", "github", 404, "no such tag"), http.StatusNotFound},
		{"credential rejected", unauthorizedErr("m", "v1.0.0", "github", 403, "bad creds"), http.StatusForbidden},
		{"no credential at all", unauthorizedErr("m", "v1.0.0", "github", 404, "no credential"), http.StatusForbidden},
		{"upstream 500", upstreamErr("m", "v1.0.0", "github", 500, "boom", nil), http.StatusBadGateway},
		{"upstream unreachable", upstreamErr("m", "v1.0.0", "github", 0, "dial failed", nil), http.StatusBadGateway},
		{"rate limited", upstreamErr("m", "v1.0.0", "github", 403, "rate limited", nil), http.StatusBadGateway},
		{"bad request", invalidErr("m", "v1.0.0", "bad escaping"), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.err.HTTPStatus())
		})
	}
}

// An error this package did not classify must never be guessed into "not
// found". Guessing absence from an unrecognized failure is exactly the
// laundering the taxonomy exists to stop.
func TestUnclassifiedErrorIsNotNotFound(t *testing.T) {
	e := asError("example.com/m", "v1.0.0", errors.New("something went sideways"))
	assert.Equal(t, KindUpstream, e.Kind)
	assert.Equal(t, http.StatusBadGateway, e.HTTPStatus())
	assert.Contains(t, e.Detail, "sideways")
}

func TestAsErrorPreservesClassification(t *testing.T) {
	orig := unauthorizedErr("example.com/m", "v1.0.0", "github", 403, "nope")
	assert.Same(t, orig, asError("other", "other", orig))

	wrapped := errors.Join(errors.New("context"), orig)
	assert.Equal(t, KindUnauthorized, asError("m", "v", wrapped).Kind)
}

// The body is the whole diagnosis: go prints a proxy's response body verbatim,
// so an authorization failure has to say plainly that it is not a missing
// module.
func TestUnauthorizedBodySaysItIsNotMissing(t *testing.T) {
	body := unauthorizedErr("github.com/org/private", "v1.2.3", "github", 404,
		"the proxy presented NO credential").Body()

	assert.Contains(t, body, "github.com/org/private")
	assert.Contains(t, body, "v1.2.3")
	assert.Contains(t, body, "HTTP 404")
	assert.Contains(t, body, "NOT a missing module")
	assert.Contains(t, body, "credential")
}

func TestNotFoundBodyDoesNotClaimCredentialTrouble(t *testing.T) {
	body := notFoundErr("github.com/org/public", "v1.2.3", "github", 404, "no such tag").Body()
	assert.Contains(t, body, "module not found")
	assert.NotContains(t, body, "NOT a missing module")
}

func TestErrorMessageNamesUpstreamStatus(t *testing.T) {
	e := upstreamErr("github.com/org/m", "v1.0.0", "github", 502, "bad gateway", nil)
	msg := e.Error()
	require.True(t, strings.Contains(msg, "github.com/org/m@v1.0.0"), msg)
	assert.Contains(t, msg, "responded 502")
}

func TestKindStrings(t *testing.T) {
	assert.Equal(t, "not_found", KindNotFound.String())
	assert.Equal(t, "unauthorized", KindUnauthorized.String())
	assert.Equal(t, "upstream", KindUpstream.String())
	assert.Equal(t, "invalid_request", KindInvalidRequest.String())
}
