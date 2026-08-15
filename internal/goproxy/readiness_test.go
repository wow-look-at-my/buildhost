package goproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// The failure readiness exists for: with no credential the proxy serves every
// PUBLIC module perfectly and no private one at all, so nothing that only asks
// "is the process up" can see it. Readiness must say so.
func TestNotReadyWithoutCredential(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "", []string{privateOrg})

	h := s.checkHealth(context.Background())

	assert.False(t, h.Healthy)
	assert.False(t, h.CredentialConfigured)
	assert.Equal(t, "none", h.CredentialKind)
	assert.Contains(t, h.Reason, "no GitHub credential")
	assert.Contains(t, h.Reason, privateOrg)
}

// A credential that merely exists is not proof it can read anything. Saying so
// out loud beats claiming a proof this check cannot make.
func TestReadyButUnprovenWithoutReadinessModule(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})

	h := s.checkHealth(context.Background())

	assert.True(t, h.Healthy)
	assert.True(t, h.CredentialConfigured)
	assert.False(t, h.Probed)
	assert.Contains(t, h.Reason, "unproven")
}

// The case the deployed proxy was actually in, one step further along: a
// credential that authenticates but is not authorized for the org. Only
// resolving a real private module catches it.
func TestNotReadyWhenReadinessModuleIsUnreadable(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Status = http.StatusNotFound
	fake.Body = `{"message":"Not Found"}`

	s := newTestService(t, fake, "a-token-without-org-access", []string{privateOrg})
	s.cfg.ReadinessModule = privateOrg + "/tml"

	h := s.checkHealth(context.Background())

	assert.False(t, h.Healthy)
	assert.True(t, h.Probed)
	assert.Contains(t, h.Reason, privateOrg+"/tml")
	assert.NotEmpty(t, h.ProbeError)
}

func TestReadyWhenReadinessModuleResolves(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	seedModule(fake, privateOrg+"/tml", "", "v1.2.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n\ngo 1.25\n")

	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.cfg.ReadinessModule = privateOrg + "/tml"

	h := s.checkHealth(context.Background())

	require.True(t, h.Healthy, h.ProbeError)
	assert.True(t, h.Probed)
	assert.Equal(t, "v1.2.0", h.ProbeVersion)
	assert.Empty(t, h.Reason)
}

// Claiming no private prefixes is a legitimate passthrough-only configuration,
// and must not report itself broken for lacking a credential it never needs.
func TestPassthroughOnlyIsHealthy(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "", nil)

	h := s.checkHealth(context.Background())

	assert.True(t, h.Healthy)
	assert.Empty(t, h.Reason)
}

// serveHealthAs drives the health endpoint, optionally as a read-scoped caller.
func serveHealthAs(t *testing.T, s *Service, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	if authed {
		req = req.WithContext(auth.WithToken(req.Context(), &db.ApiToken{Scopes: "read"}))
	}
	rec := httptest.NewRecorder()
	s.serveHealth(rec, req)
	return rec
}

// The health endpoint answers 503 when the proxy cannot serve what it claims,
// so an external check sees the difference rather than only "the port is open".
func TestHealthEndpointReports503WhenNotReady(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "", []string{privateOrg})
	s.checkHealth(context.Background())

	rec := serveHealthAs(t, s, true)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var h Health
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &h))
	assert.False(t, h.Healthy)
	assert.Equal(t, "none", h.CredentialKind)
}

func TestHealthEndpointReports200WhenReady(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.checkHealth(context.Background())

	assert.Equal(t, http.StatusOK, serveHealthAs(t, s, false).Code)
	assert.Equal(t, http.StatusOK, serveHealthAs(t, s, true).Code)
}

// serve() gates even public modules so nobody can ask anonymously which modules
// are private. The health endpoint must not answer that same question for free:
// the prefixes, the readiness module and a probe error all name private repos.
// The verdict stays public so a monitor needs no credential.
func TestHealthEndpointRedactsPrivateNamesFromAnonymousCallers(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Status = http.StatusNotFound
	fake.Body = `{"message":"Not Found"}`
	s := newTestService(t, fake, "a-token-without-org-access", []string{privateOrg})
	s.cfg.ReadinessModule = privateOrg + "/tml"
	s.checkHealth(context.Background())

	rec := serveHealthAs(t, s, false)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"the verdict itself stays unauthenticated -- a monitor that must authenticate is one nobody wires up")

	body := rec.Body.String()
	assert.Contains(t, body, `"healthy":false`)
	assert.NotContains(t, body, privateOrg, "the private prefix list must not leak to an anonymous caller")
	assert.NotContains(t, body, "tml", "the readiness module names a private repository")
	assert.NotContains(t, body, "credential_kind")

	// The same request WITH a read token gets everything, so nothing an operator
	// needs is actually hidden.
	authed := serveHealthAs(t, s, true).Body.String()
	assert.Contains(t, authed, privateOrg+"/tml")
	assert.Contains(t, authed, "credential_kind")
}

// The dashboard's snapshot has to survive an empty cache: a proxy that has
// never served anything still needs its health shown.
func TestSnapshotOnEmptyCache(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.checkHealth(context.Background())

	st, err := s.Snapshot(context.Background())
	require.NoError(t, err)

	assert.Empty(t, st.Modules)
	assert.Empty(t, st.Recent)
	assert.Zero(t, st.Cache.Modules)
	assert.True(t, st.Traffic.SinceStart)
	assert.True(t, st.Health.CredentialConfigured)
}

func TestSnapshotSurfacesAFailingModule(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	s := newTestService(t, fake, "", []string{privateOrg})

	require.Equal(t, http.StatusForbidden,
		serveProxy(t, s, "/"+privateOrg+"/tml/@v/v1.0.0.info").Code)

	st, err := s.Snapshot(context.Background())
	require.NoError(t, err)

	require.Len(t, st.Modules, 1)
	assert.Equal(t, "unauthorized", st.Modules[0].LastErrorKind)
	assert.True(t, st.Modules[0].Private)
	assert.EqualValues(t, 1, st.Cache.FailingModules)

	require.NotEmpty(t, st.Recent)
	assert.Equal(t, "error", st.Recent[0].Outcome)
	assert.Equal(t, http.StatusForbidden, st.Recent[0].Status)
}

func TestLoadConfigDefaultsPrivatePrefixesFromOIDCOrgs(t *testing.T) {
	c := loadConfig([]string{"wow-look-at-my", "PazerOP"})
	assert.Equal(t, []string{"github.com/wow-look-at-my", "github.com/PazerOP"}, c.PrivatePrefixes)
}

// No third-party mirror unless an operator asks for one. A mirror sees the path
// of every dependency routed through it, so a default here would ship the org's
// dependency graph to someone else without anyone choosing that.
func TestLoadConfigConfiguresNoUpstreamByDefault(t *testing.T) {
	c := loadConfig([]string{"wow-look-at-my"})
	assert.Empty(t, c.Upstream, "buildhost must not pick a module mirror on the operator's behalf")
}

func TestLoadConfigExplicitPrefixesWin(t *testing.T) {
	t.Setenv("BUILDHOST_GOPROXY_PRIVATE_PREFIXES", "github.com/a, github.com/b/ ")
	t.Setenv("BUILDHOST_GOPROXY_UPSTREAM", "https://mirror.example.com/")

	c := loadConfig([]string{"ignored"})

	assert.Equal(t, []string{"github.com/a", "github.com/b"}, c.PrivatePrefixes)
	assert.Equal(t, "https://mirror.example.com", c.Upstream)
}

// A passthrough-only proxy has nothing a credential is needed for, so readiness
// is settled inline and no background loop starts. Every test in this repo that
// calls auth.Init has that shape, and a per-call ticker would outlive the test.
func TestPassthroughOnlyStartsNoBackgroundLoop(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "", nil)

	before := runtime.NumGoroutine()
	s.startReadiness(context.Background())

	// Health is populated by the time startReadiness returns, with no goroutine
	// left running behind it.
	assert.True(t, s.Health().Healthy)
	assert.False(t, s.Health().CheckedAt.IsZero())
	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}
