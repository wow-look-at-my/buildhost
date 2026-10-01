package admin

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestAdminSetVersioning(t *testing.T) {
	t.Serial()
	srv, d := newTestServer(t)
	ctx := context.Background()

	p := &db.Project{Name: "vproj", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, p))
	require.NoError(t, d.CreateRelease(ctx, &db.Release{ProjectID: p.ID, Version: "2.3.0", VersionNum: 2_003_000, GitBranch: "master"}))

	rec := serve(srv, "PUT", "/api/projects/vproj/versioning", bytes.NewBufferString(`{"versioning":"calver"}`))
	assert.Equal(t, 400, rec.Code)

	rec = serve(srv, "PUT", "/api/projects/missing/versioning", bytes.NewBufferString(`{"versioning":"auto"}`))
	assert.Equal(t, 404, rec.Code)

	rec = serve(srv, "PUT", "/api/projects/vproj/versioning", bytes.NewBufferString(`{"versioning":"auto"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	got, err := d.GetProject(ctx, "vproj")
	require.NoError(t, err)
	assert.Equal(t, db.VersioningAuto, got.Versioning)

	next, err := d.NextVersionNum(ctx, p.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2_003_001), next, "an auto project continues past the semver numbers")

	rec = serve(srv, "PUT", "/api/projects/vproj/versioning", bytes.NewBufferString(`{"versioning":"semver"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	got, err = d.GetProject(ctx, "vproj")
	require.NoError(t, err)
	assert.Equal(t, db.VersioningSemver, got.Versioning)
}
