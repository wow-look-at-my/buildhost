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

// routesAtStartup is the table as it stands before any test runs. ListRoutes
// renders configured domains into it once auth.Init has run, and a test in this
// package calls Init, so the golden cannot be rendered from inside a test.
var routesAtStartup string

func TestMain(m *testing.M) {
	routesAtStartup = renderRoutes()
	os.Exit(m.Run())
}

// TestRouteTableMatchesGolden fails when the route set drifts from
// docs/routes.txt. The golden file is what makes a route change REVIEWABLE: this
// repo has no central route table -- backends self-register from init() in their
// own packages -- so without it, adding, removing or duplicating an endpoint
// leaves nothing route-shaped in the diff for a reviewer to look at.
//
// The table is rendered by the program, never parsed out of source, so it cannot
func TestRouteTableMatchesGolden(t *testing.T) {
	t.Serial()
	got := routesAtStartup
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
	t.Serial()
	lines := strings.Split(strings.TrimSuffix(routesAtStartup, "\n"), "\n")
	require.NotEmpty(t, lines)
	require.IsIncreasing(t, lines)
}
