package routescheck

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// goldenPath is the committed route table, relative to this package.
const goldenPath = "../../docs/routes.txt"

// regenHint is printed on every failure so the fix never needs looking up.
const regenHint = "regenerate with:  UPDATE_ROUTES_GOLDEN=1 go-toolchain   (or, from a built binary:  ./build/buildhost routes > docs/routes.txt)"

// updateGolden rewrites the golden instead of asserting against it. The env var
func updateGolden() bool { return os.Getenv("UPDATE_ROUTES_GOLDEN") == "1" }

func renderRoutes() string {
	var b strings.Builder
	for _, r := range auth.ListRoutes() {
		b.WriteString(r.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// assertNotInitialized guards the ordering renderRoutes depends on. Test order
// within a package follows file name, so golden_test.go currently runs before
// routes_test.go calls auth.Init -- a fact no reader of either file can see.
func assertNotInitialized(t *testing.T) {
	t.Helper()
	require.Empty(t, auth.SiteDomain(),
		"auth.Init already ran, so ListRoutes would render real-domain routes into the golden; this test must run before any test that calls auth.Init")
}

// TestRouteTableMatchesGolden fails when the route set drifts from
// docs/routes.txt. The golden file is what makes a route change REVIEWABLE: this
// repo has no central route table -- backends self-register from init() in their
// own packages -- so without it, adding, removing or duplicating an endpoint
// leaves nothing route-shaped in the diff for a reviewer to look at.
//
// The table is rendered by the program, never parsed out of source, so it cannot
func TestRouteTableMatchesGolden(t *testing.T) {
	assertNotInitialized(t)
	got := renderRoutes()
	if updateGolden() {
		require.NoError(t, os.WriteFile(goldenPath, []byte(got), 0o644))
		t.Log("rewrote " + goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "read %s -- %s", goldenPath, regenHint)
	require.Equal(t, string(want), got,
		"the route table changed but %s was not updated.\n%s", goldenPath, regenHint)
}

// TestGoldenRouteTableIsSorted pins the ordering the golden file relies on: a
func TestGoldenRouteTableIsSorted(t *testing.T) {
	assertNotInitialized(t)
	lines := strings.Split(strings.TrimSuffix(renderRoutes(), "\n"), "\n")
	require.NotEmpty(t, lines)
	require.IsIncreasing(t, lines)
}
