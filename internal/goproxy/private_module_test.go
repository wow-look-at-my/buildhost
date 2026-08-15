package goproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

const privateOrg = "github.com/wow-look-at-my"

// serveProxy drives one request through the real handler. The auth gate is
// satisfied with a read token in the request context, which is what the global
// Authenticate middleware puts there in production.
func serveProxy(t *testing.T, s *Service, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(auth.WithToken(req.Context(), &db.ApiToken{Scopes: "read"}))
	rec := httptest.NewRecorder()
	s.serve(rec, req)
	return rec
}

// The regression test the whole change exists for: a PRIVATE first-party module
// must actually resolve. Every pre-existing test in the old proxy used a public
// fixture, which is precisely why a proxy serving no private module at all
// looked healthy.
func TestPrivateModuleResolves(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	seedModule(fake, privateOrg+"/tml", "", "v1.2.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n\ngo 1.25\n")

	s := newTestService(t, fake, "a-working-token", []string{privateOrg})

	t.Run("list", func(t *testing.T) {
		rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/list")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "v1.2.0\n", rec.Body.String())
	})

	t.Run("info", func(t *testing.T) {
		rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.2.0.info")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"Version":"v1.2.0"`)
	})

	t.Run("mod", func(t *testing.T) {
		rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.2.0.mod")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "module "+privateOrg+"/tml")
	})

	t.Run("zip", func(t *testing.T) {
		rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.2.0.zip")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
		assert.NotZero(t, rec.Body.Len())
	})

	t.Run("latest", func(t *testing.T) {
		rec := serveProxy(t, s, "/"+privateOrg+"/tml/@latest")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"Version":"v1.2.0"`)
	})
}

// The reported defect, as a test. With no credential the proxy must NOT answer
// 404: at the protocol level that means "this module does not exist", and it is
// what sent people looking for a typo in go.mod instead of at the credential.
func TestPrivateModuleWithoutCredentialIsForbiddenNotNotFound(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	seedModule(fake, privateOrg+"/tml", "", "v1.2.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n\ngo 1.25\n")

	s := newTestService(t, fake, "", []string{privateOrg})

	for _, path := range []string{
		"/" + privateOrg + "/tml/@v/list",
		"/" + privateOrg + "/tml/@latest",
		"/" + privateOrg + "/tml/@v/v1.2.0.info",
		"/" + privateOrg + "/tml/@v/v1.2.0.mod",
		"/" + privateOrg + "/tml/@v/v1.2.0.zip",
	} {
		t.Run(path, func(t *testing.T) {
			rec := serveProxy(t, s, path)

			require.NotEqual(t, http.StatusNotFound, rec.Code,
				"an unreadable private module must never be reported as missing")
			assert.Equal(t, http.StatusForbidden, rec.Code)

			body := rec.Body.String()
			assert.Contains(t, body, "NOT a missing module")
			assert.Contains(t, body, "NO GitHub credential")
			assert.NotEmpty(t, body, "the empty-body 404 is the defect being fixed")
		})
	}
}

// A public module that genuinely is not there still 404s -- the fix must not
// turn every miss into a 403.
func TestGenuinelyMissingModuleStill404s(t *testing.T) {
	fake := newFakeGitHub(t)
	seedModule(fake, privateOrg+"/tml", "", "v1.2.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n\ngo 1.25\n")

	s := newTestService(t, fake, "a-working-token", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/v9.9.9.info")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "module not found")
}

// GitHub answers a rate limit with 403, which is not an authorization problem:
// it clears on its own, and reporting it as one sends the reader after a
// credential that was never at fault.
func TestRateLimitIsUpstreamNotUnauthorized(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Status = http.StatusForbidden
	fake.RateLimited = true
	fake.Body = `{"message":"API rate limit exceeded"}`

	s := newTestService(t, fake, "a-working-token", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/list")
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "rate limited")
}

func TestUpstreamServerErrorIsBadGateway(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Status = http.StatusInternalServerError
	fake.Body = `{"message":"server error"}`

	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/list")
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "upstream fetch failed")
}

// A rejected credential (401/403 proper) is an authorization failure, and it
// must say the credential was rejected rather than that nothing was presented.
func TestRejectedCredentialSaysSo(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Status = http.StatusUnauthorized
	fake.Body = `{"message":"Bad credentials"}`

	s := newTestService(t, fake, "a-stale-token", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/list")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "credential was rejected")
}

// The proxy serves private source, so an unauthenticated caller gets 401 before
// anything reaches upstream -- including for a public module, so that "is this
// module private?" is not an oracle anybody can query anonymously.
// serveAnon drives the proxy with no credential at all.
func serveAnon(t *testing.T, s *Service, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.serve(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// A module the caller may not see is 404, never 401 or 403: either of those
// confirms it EXISTS, which is the fact a private module is keeping.
func TestInaccessibleModuleIs404NotUnauthorized(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	seedModule(fake, privateOrg+"/tml", "", "v1.0.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n")
	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveAnon(t, s, "/"+privateOrg+"/tml/@v/list")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
	assert.NotEqual(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("WWW-Authenticate"),
		"a challenge would announce that something is there to authenticate for")
	assert.Zero(t, fake.calls, "an unauthorized caller must not reach upstream")
}

// The property that makes the 404 worth anything: a private module that EXISTS
// and one that does not must be answered identically, or a prober maps the org's
// private repositories by diffing responses.
func TestExistingAndMissingPrivateModulesAreIndistinguishable(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	seedModule(fake, privateOrg+"/tml", "", "v1.0.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n")
	s := newTestService(t, fake, "tok", []string{privateOrg})

	real := serveAnon(t, s, "/"+privateOrg+"/tml/@v/list")
	fictional := serveAnon(t, s, "/"+privateOrg+"/no-such-repo-anywhere/@v/list")

	assert.Equal(t, real.Code, fictional.Code)
	assert.Equal(t, real.Header().Get("Content-Type"), fictional.Header().Get("Content-Type"))
	// Only the echoed module path may differ, so compare with it removed.
	assert.Equal(t,
		strings.ReplaceAll(real.Body.String(), privateOrg+"/tml", "M"),
		strings.ReplaceAll(fictional.Body.String(), privateOrg+"/no-such-repo-anywhere", "M"))
}

// A PROJECT-scoped token says "this job may read project X". A Go module is not
// a project, so honouring it here would widen a least-privilege credential to
// the org's whole private source tree.
func TestProjectScopedTokenCannotReadPrivateModules(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	s := newTestService(t, fake, "tok", []string{privateOrg})

	projectID := int64(1)
	req := httptest.NewRequest(http.MethodGet, "/"+privateOrg+"/tml/@v/list", nil)
	req = req.WithContext(auth.WithToken(req.Context(), &db.ApiToken{Scopes: "read", ProjectID: &projectID}))
	rec := httptest.NewRecorder()
	s.serve(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Zero(t, fake.calls)
}

// The counterpart: gating PUBLIC modules would only stop GOPROXY=<proxy>,direct
// working for anyone without a buildhost token, and there is nothing to hide.
func TestPublicModuleNeedsNoCredential(t *testing.T) {
	mirror := fakeMirror(t, map[string]string{"golang.org/x/mod/@v/list": "v0.40.0\n"})
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.upstream = newUpstreamSource(s.github.client, mirror.URL, []string{privateOrg})

	rec := serveAnon(t, s, "/golang.org/x/mod/@v/list")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "v0.40.0\n", rec.Body.String())
}

// The distinction the whole error taxonomy rests on, in one test: the caller's
// credential missing is a 404, the PROXY's credential failing is a 403. Losing
// the second one is the original bug -- nobody can fix a proxy credential they
// cannot see, and 404 tells them to go hunt a typo in go.mod instead.
func TestProxyCredentialFailureStays403WhileCallerFailureIs404(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	s := newTestService(t, fake, "", []string{privateOrg}) // proxy has NO credential

	assert.Equal(t, http.StatusNotFound, serveAnon(t, s, "/"+privateOrg+"/tml/@v/list").Code,
		"caller has no credential: the module's existence is not theirs to learn")

	authed := serveProxy(t, s, "/"+privateOrg+"/tml/@v/list")
	require.Equal(t, http.StatusForbidden, authed.Code,
		"proxy has no credential: that is an operator failure and must be loud")
	assert.Contains(t, authed.Body.String(), "NO GitHub credential")
}

// A failing module records why on its own row, so the dashboard shows a
// credential problem instead of it living only in a log line.
func TestFailureIsRecordedAgainstTheModule(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	s := newTestService(t, fake, "", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.0.0.info")
	require.Equal(t, http.StatusForbidden, rec.Code)

	mods, err := s.db.ListGoproxyModules(context.Background())
	require.NoError(t, err)
	require.Len(t, mods, 1)
	assert.Equal(t, privateOrg+"/tml", mods[0].ModulePath)
	assert.Equal(t, "unauthorized", mods[0].LastErrorKind)
	assert.Contains(t, mods[0].LastError, "NO GitHub credential")
}

// A second request for a version already cached must not go upstream again.
func TestSecondRequestIsACacheHit(t *testing.T) {
	fake := newFakeGitHub(t)
	seedModule(fake, privateOrg+"/tml", "", "v1.2.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n\ngo 1.25\n")
	s := newTestService(t, fake, "tok", []string{privateOrg})

	require.Equal(t, http.StatusOK, serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.2.0.mod").Code)
	after := fake.calls
	require.NotZero(t, after)

	require.Equal(t, http.StatusOK, serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.2.0.mod").Code)
	assert.Equal(t, after, fake.calls, "a cached version must not be re-fetched")

	hits, _, _, _, _, _ := s.metrics.snapshot()
	assert.Positive(t, hits)
}

// A module in a repo subdirectory tags its versions "<dir>/vX.Y.Z". This is a
// real shape in the org, and getting it wrong makes the module unresolvable.
func TestNestedModuleUsesTagPrefix(t *testing.T) {
	fake := newFakeGitHub(t)
	modPath := privateOrg + "/agentic-loop/go"
	seedModule(fake, modPath, "go", "go/v0.3.0", "bbbb111122223333444455556666777788889999",
		"module "+modPath+"\n\ngo 1.25\n")
	// A tag on the repo root is a different module's version and must be ignored.
	fake.Tags["v9.9.9"] = "cccc111122223333444455556666777788889999"

	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+modPath+"/@v/list")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "v0.3.0\n", rec.Body.String())
}

// With no tags at all -- the normal state for this org's branch-pinned modules
// -- @latest must resolve to a pseudo-version of the default branch head rather
// than reporting that there is nothing there.
func TestLatestFallsBackToPseudoVersion(t *testing.T) {
	fake := newFakeGitHub(t)
	modPath := privateOrg + "/untagged"
	fake.Files["HEAD:go.mod"] = "module " + modPath + "\n"
	fake.Files["headshaaaaaa0000000000000000000000000000:go.mod"] = "module " + modPath + "\n"

	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+modPath+"/@latest")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "v0.0.0-")
	assert.Contains(t, rec.Body.String(), "headshaaaaaa")
}

// A go.mod that declares a different module path means this repo directory is
// not that module.
func TestMismatchedGoModIsNotFound(t *testing.T) {
	fake := newFakeGitHub(t)
	modPath := privateOrg + "/tml"
	seedModule(fake, modPath, "", "v1.0.0", "aaaa111122223333444455556666777788889999",
		"module github.com/somebody/else\n")

	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+modPath+"/@v/list")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "declares module github.com/somebody/else")
}

// Pseudo-versions are how this org pins its untagged first-party modules (the
// go-toolchain branch pins produce them), so resolving one by the commit it
// embeds is a primary path, not an edge case.
func TestPseudoVersionResolvesByEmbeddedRevision(t *testing.T) {
	fake := newFakeGitHub(t)
	modPath := privateOrg + "/router"
	const sha = "302008ab124800000000000000000000000000ff"
	fake.Files["HEAD:go.mod"] = "module " + modPath + "\n"
	fake.Files["302008ab1248:go.mod"] = "module " + modPath + "\n"
	fake.Files[sha+":go.mod"] = "module " + modPath + "\n"
	fake.TreeFiles["go.mod"] = "module " + modPath + "\n"
	fake.TreeFiles["router.go"] = "package router\n"

	const version = "v0.0.0-20260721161008-302008ab1248"
	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+modPath+"/@v/"+version+".info")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), version)

	rec = serveProxy(t, s, "/"+modPath+"/@v/"+version+".zip")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotZero(t, rec.Body.Len())
}

// A version string that is not semver at all is a malformed request, not a
// missing module -- the caller has to fix their request, not go looking for a
// version that was never there.
func TestNonSemverVersionIsABadRequest(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/router/@v/1.2.3.info")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "not a valid semantic version")
}

// A timestamp-only prerelease is NOT a pseudo-version: it carries no revision,
// so it addresses no commit and is resolved as an ordinary tag -- which is
// absent, and correctly reported as such.
func TestTimestampPrereleaseIsResolvedAsATag(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Files["HEAD:go.mod"] = "module " + privateOrg + "/router\n"
	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/router/@v/v0.0.0-20260721161008.info")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "no tag")
}
