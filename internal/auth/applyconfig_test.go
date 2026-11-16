package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

// saveShared puts the settings ApplyConfig replaces back when the test ends.
func saveShared(t *testing.T) {
	t.Helper()
	origMW := mw
	fetch, secret, orgs, site, primary := sharedFetchDomains, sharedGitHubWebhookSecret, sharedOIDCOrgs, sharedSiteDomain, sharedPrimaryDomain
	t.Cleanup(func() {
		mw = origMW
		sharedFetchDomains, sharedGitHubWebhookSecret, sharedOIDCOrgs, sharedSiteDomain, sharedPrimaryDomain = fetch, secret, orgs, site, primary
	})
}

func TestApplyConfigReplacesTheSettingsInUse(t *testing.T) {
	t.Serial()
	saveShared(t)
	mw = &Middleware{Verifier: NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{"https://old.example"}})}

	cfg := config.Config{
		PrimaryDomain:       "Apply.Example",
		SiteFetchDomains:    []string{"fetch.example"},
		GitHubWebhookSecret: "hook",
		OIDCIssuers:         []string{"https://new.example"},
		OIDCOrgs:            []string{"org"},
		OIDCEvents:          []string{"push"},
		GitHubClientID:      "id",
		GitHubClientSecret:  "secret",
	}
	ApplyConfig(cfg)
	assert.Equal(t, "apply.example", PrimaryDomain())
	assert.Equal(t, []string{"fetch.example"}, SiteFetchDomains())
	assert.Equal(t, "hook", GitHubWebhookSecret())
	assert.Equal(t, []string{"org"}, OIDCOrgs())
	assert.Equal(t, OIDCConfig{TrustedIssuers: []string{"https://new.example"}, AllowedOrgs: []string{"org"}, AllowedEvents: []string{"push"}}, mw.Verifier.policy())

	gh := githubAuth()
	require.NotNil(t, gh, "a client id and secret turn GitHub sign-in on")
	cfg.PrimaryDomain = "other.example"
	ApplyConfig(cfg)
	assert.Same(t, gh, githubAuth(), "an unrelated change keeps the sign-in and its repo cache")

	cfg.GitHubClientSecret = "rotated"
	ApplyConfig(cfg)
	assert.NotSame(t, gh, githubAuth(), "a new client secret replaces the sign-in")

	cfg.GitHubClientID = ""
	ApplyConfig(cfg)
	assert.Nil(t, githubAuth(), "an empty client id turns GitHub sign-in off")
}

func TestApplyConfigServesANewSiteDomainAndRetiresTheOldOne(t *testing.T) {
	t.Serial()
	saveShared(t)
	const first, second = "apply-first.example", "apply-second.example"
	hit := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	// The same registration a backend makes from an OnSiteDomain function.
	SiteDomainHandleRaw(first, "GET /apply-probe", hit)
	SiteDomainHandleRaw(second, "GET /apply-probe", hit)

	probe := func(domain string) int {
		r := httptest.NewRequest(http.MethodGet, "https://proj."+domain+"/apply-probe", nil)
		w := httptest.NewRecorder()
		ServeHTTP(w, r)
		return w.Code
	}
	hasRoute := func(domain string) bool {
		for _, rt := range mux.Routes() {
			if strings.Contains(rt.Pattern, "{project}."+domain+ssoPath) {
				return true
			}
		}
		return false
	}

	ApplyConfig(config.Config{PrimaryDomain: "apply.example", SiteDomain: first})
	assert.Equal(t, first, SiteDomain())
	assert.True(t, hasRoute(first), "the routes of a new site domain register when it is applied")
	assert.Equal(t, http.StatusNoContent, probe(first))
	assert.Equal(t, http.StatusNotFound, probe(second), "a domain that is not configured answers 404")

	ApplyConfig(config.Config{PrimaryDomain: "apply.example", SiteDomain: second})
	assert.True(t, hasRoute(second))
	assert.Equal(t, http.StatusNoContent, probe(second))
	assert.Equal(t, http.StatusNotFound, probe(first), "the old domain stops serving with no restart")

	ApplyConfig(config.Config{PrimaryDomain: "apply.example"})
	assert.Equal(t, http.StatusNotFound, probe(second), "an empty site domain serves no site routes")
}
