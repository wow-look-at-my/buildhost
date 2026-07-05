package routescheck

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/auth"

	// Import every backend so its init() registers routes. The guard below
	// then sees the full route table. auth.Init is deliberately NOT called.
	_ "github.com/wow-look-at-my/buildhost/internal/api"
	_ "github.com/wow-look-at-my/buildhost/internal/apt"
	_ "github.com/wow-look-at-my/buildhost/internal/brew"
	_ "github.com/wow-look-at-my/buildhost/internal/dl"
	_ "github.com/wow-look-at-my/buildhost/internal/llms"
	_ "github.com/wow-look-at-my/buildhost/internal/npm"
	_ "github.com/wow-look-at-my/buildhost/internal/oci"
	_ "github.com/wow-look-at-my/buildhost/internal/sites"
	_ "github.com/wow-look-at-my/buildhost/internal/static"
	_ "github.com/wow-look-at-my/buildhost/internal/web"
)

// TestAllRoutesRegisteredWithoutInit guards against a backend registering its
// routes inside an auth.OnReady() callback instead of directly in init().
// OnReady callbacks fire only from auth.Init() (server startup), so such routes
// are invisible to `buildhost routes`, the route-diff CI check, and any tooling
// that enumerates routes without booting a server. Every backend must call
// auth.ServiceHandle/Handle in init() and use OnReady only to populate handler
// dependencies (DB, Store, ...).
//
// This package imports every backend (their init() runs) but never calls
// auth.Init(), so a route deferred to OnReady drops out of auth.AllRoutes()
// and fails here.
func TestAllRoutesRegisteredWithoutInit(t *testing.T) {
	var patterns []string
	for _, r := range auth.AllRoutes() {
		patterns = append(patterns, r.Pattern)
	}

	// One representative route per service-subdomain backend.
	want := []string{
		"npm.{domain}/{pkg}",
		"npm.{domain}/@buildhost/{project}/-/{filename}",
		"apt.{domain}/{path...}",
		"brew.{domain}/{project}",
		"git.{domain}/brew/tap.git/{path...}",
		"dl.{domain}/{project}",
		"sites.{domain}/{project}/branch/{branch}",
		"static.{domain}/file",
		"oci.{domain}/v2/",
	}
	for _, w := range want {
		assert.Contains(t, patterns, w,
			"route %q missing from auth.AllRoutes(); is it registered inside auth.OnReady() instead of directly in init()? Found:\n%s",
			w, strings.Join(patterns, "\n"))
	}
}
