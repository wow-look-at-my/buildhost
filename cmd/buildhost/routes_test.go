package main

import (
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/testify/assert"
)

// TestAllRoutesRegisteredWithoutInit guards against backends that register
// their routes inside an auth.OnReady() callback instead of directly in
// init(). OnReady callbacks only fire from auth.Init() (server startup), so
// such routes are invisible to `buildhost routes`, the route-diff CI check,
// and any tooling that enumerates routes without booting a server. Every
// backend must call auth.ServiceHandle/Handle in init() and only populate
// handler dependencies (DB, Store, ...) inside OnReady.
//
// This test runs in package main, so all backend_*.go imports' init()
// functions have already fired, but auth.Init() has NOT been called.
func TestAllRoutesRegisteredWithoutInit(t *testing.T) {
	var patterns []string
	for _, r := range auth.AllRoutes() {
		patterns = append(patterns, r.Pattern)
	}

	// One representative route per service-subdomain backend. If any backend
	// regresses to OnReady-time registration, its routes drop out here.
	want := []string{
		"npm.*/@buildhost/{project}",
		"npm.*/@buildhost/{project}/-/{filename}",
		"apt.*/{path...}",
		"brew.*/{project}",
		"dl.*/{project}",
		"sites.*/{project}/branch/{branch}",
		"static.*/file",
		"oci.*/v2/",
	}
	for _, w := range want {
		assert.Contains(t, patterns, w,
			"route %q missing from auth.AllRoutes(); is it registered inside auth.OnReady() instead of directly in init()? Found:\n%s",
			w, strings.Join(patterns, "\n"))
	}
}
