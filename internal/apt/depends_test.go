package apt

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServePackages_CarriesDepends(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, _, _ := seedAptProject(t, d, store, "dats", "binary-bytes", false)
	require.NoError(t, d.SetProjectAptDepends(ctx, proj.ID, "bubblewrap | docker.io"))
	proj.AptDepends = "bubblewrap | docker.io"

	rec := getPackages(t, h, "dats", proj)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "\nDepends: bubblewrap | docker.io\n")
}

func TestServePackages_NoDependsNoLine(t *testing.T) {
	h, d, store := setupTest(t)
	proj, _, _ := seedAptProject(t, d, store, "myapp", "binary-bytes", false)

	rec := getPackages(t, h, "myapp", proj)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Depends:")
}

func TestServePackages_RefillsOnDependsChange(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()
	proj, _, a := seedAptProject(t, d, store, "myapp", "binary-bytes", false)

	getPackages(t, h, "myapp", proj)
	_, _, before, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.NoError(t, err)

	require.NoError(t, d.SetProjectAptDepends(ctx, proj.ID, "bubblewrap"))
	proj.AptDepends = "bubblewrap"

	body := getPackages(t, h, "myapp", proj).Body.String()
	_, _, after, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	require.NoError(t, err)
	assert.NotEqual(t, before, after, "the Depends line changes the deb bytes, so the cached digest must refill")
	assert.Contains(t, body, fmt.Sprintf("SHA256: %s\n", after))
}

func TestServePackages_InvalidStoredDependsFailsLoud(t *testing.T) {
	h, d, store := setupTest(t)
	proj, _, _ := seedAptProject(t, d, store, "myapp", "binary-bytes", false)
	proj.AptDepends = "bubblewrap\nEssential: yes"

	assert.Equal(t, http.StatusInternalServerError, getPackages(t, h, "myapp", proj).Code)
}
