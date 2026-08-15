package routescheck

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/router"

	// Import every backend so its init() registers routes. The guard below
	// then sees the full route table.
	_ "github.com/wow-look-at-my/buildhost/internal/api"
	_ "github.com/wow-look-at-my/buildhost/internal/apt"
	_ "github.com/wow-look-at-my/buildhost/internal/brew"
	_ "github.com/wow-look-at-my/buildhost/internal/dl"
	_ "github.com/wow-look-at-my/buildhost/internal/goproxy"
	_ "github.com/wow-look-at-my/buildhost/internal/llms"
	_ "github.com/wow-look-at-my/buildhost/internal/npm"
	_ "github.com/wow-look-at-my/buildhost/internal/oci"
	_ "github.com/wow-look-at-my/buildhost/internal/sites"
	_ "github.com/wow-look-at-my/buildhost/internal/static"
	_ "github.com/wow-look-at-my/buildhost/internal/uploads"
	_ "github.com/wow-look-at-my/buildhost/internal/web"
)

const testSiteDomain = "routecheck.example"

func patterns(routes []router.Route) []string {
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.Pattern)
	}
	sort.Strings(out)
	return out
}

// TestInitRegistersOnlySiteDomainRoutes is the exhaustive form of the guard:
// it compares the enumerable route table against the one a booted server has,
// so ANY route reachable only after auth.Init fails -- no allowlist of
// representative routes to fall out of date, and no new backend that has to
// remember to add itself.
//
// A route registered inside an auth.OnReady callback exists only in a running
// server: `buildhost routes` never prints it, so the route-diff CI job never
// shows it on a PR and it can change with nobody seeing the change. The one
// sanctioned exception is a pattern that genuinely depends on configuration;
// it registers through auth.OnSiteDomain, which auth.ListRoutes also runs
// against auth.SiteDomainPlaceholder, keeping it enumerable.
func TestInitRegistersOnlySiteDomainRoutes(t *testing.T) {
	enumerable := patterns(auth.ListRoutes())
	enumerableSet := map[string]bool{}
	for _, p := range enumerable {
		enumerableSet[p] = true
	}

	auth.Init(nil, nil, t.TempDir(), nil, nil, nil, nil, "", "", "", testSiteDomain, "primary.example")

	for _, p := range patterns(auth.AllRoutes()) {
		// A configured site-domain route is the placeholder route with the real
		// domain substituted back in.
		if enumerableSet[strings.ReplaceAll(p, testSiteDomain, auth.SiteDomainPlaceholder)] {
			continue
		}
		assert.Fail(t, "route is invisible to `buildhost routes`",
			"route %q appeared only after auth.Init. Register it in init() instead of inside "+
				"auth.OnReady (OnReady is for wiring handler dependencies), or -- if its pattern "+
				"genuinely depends on configuration -- through auth.OnSiteDomain. Enumerable routes:\n%s",
			p, strings.Join(enumerable, "\n"))

	}
}

// TestListRoutesCoversConfigConditionalFamilies pins the other half: the
// enumerable table must actually contain the config-conditional routes, so a
// future refactor cannot quietly drop them out of `buildhost routes` and leave
// the check above trivially satisfied.
func TestListRoutesCoversConfigConditionalFamilies(t *testing.T) {
	got := patterns(auth.ListRoutes())
	for _, want := range []string{
		"/__sso",
		"{project}." + auth.SiteDomainPlaceholder + "/__sso",
		"{project}." + auth.SiteDomainPlaceholder + "/{path...}",
	} {
		assert.Contains(t, got, want)
	}
}
