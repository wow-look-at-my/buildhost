package db

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAptDepends_Accepts(t *testing.T) {
	t.Parallel()
	for _, v := range []string{
		"",
		"bubblewrap",
		"bubblewrap | docker.io",
		"bubblewrap|docker.io",
		"bubblewrap | docker.io, curl",
		"libc6 (>= 2.34), git (<< 3:0)",
		"python3:any",
		"foo [amd64 !arm64] <!nocheck>",
		"g++, libstdc++6 (= 1.0-1~bpo1)",
	} {
		assert.NoError(t, ValidateAptDepends(v), "%q is valid Debian relationship syntax", v)
	}
}

func TestValidateAptDepends_RejectsInjection(t *testing.T) {
	t.Parallel()
	for _, v := range []string{
		"bubblewrap\nDescription: owned",
		"bubblewrap\r\nPre-Depends: evil",
		"bubblewrap\n",
		"Pre-Depends: evil",
		"bubblewrap, Conflicts: dats",
		"bubblewrap; rm -rf /",
		"$(touch /tmp/x)",
		"bubblewrap,",
		"| docker.io",
		"a || b",
		"Bubblewrap",
		"x",
		"foo (>= )",
		"foo (~ 1.0)",
		strings.Repeat("a", MaxAptDependsLen+1),
	} {
		assert.Error(t, ValidateAptDepends(v), "%q must be refused", v)
	}
}

func TestSetProjectAptDepends_RefusesInvalidAndStoresNothing(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "depproj", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	require.NoError(t, d.SetProjectAptDepends(ctx, p.ID, "bubblewrap | docker.io"))
	got, err := d.GetProject(ctx, "depproj")
	require.NoError(t, err)
	assert.Equal(t, "bubblewrap | docker.io", got.AptDepends)

	require.Error(t, d.SetProjectAptDepends(ctx, p.ID, "bubblewrap\nEssential: yes"))
	got, err = d.GetProject(ctx, "depproj")
	require.NoError(t, err)
	assert.Equal(t, "bubblewrap | docker.io", got.AptDepends, "a refused value leaves the stored one in place")

	require.NoError(t, d.SetProjectAptDepends(ctx, p.ID, ""))
	got, err = d.GetProject(ctx, "depproj")
	require.NoError(t, err)
	assert.Empty(t, got.AptDepends)
}
