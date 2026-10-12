package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestAdminVisibility(t *testing.T) {
	t.Serial()
	srv, d := newTestServer(t)
	ctx := context.Background()

	p := &db.Project{Name: "org/app", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))
	rel := &db.Release{ProjectID: p.ID, Version: "1", VersionNum: 1, GitBranch: "master"}
	require.NoError(t, d.CreateRelease(ctx, rel))
	escaped := url.PathEscape(p.Name)

	isPrivate := func() bool {
		got, err := d.GetProject(ctx, p.Name)
		require.NoError(t, err)
		return got.IsPrivate
	}

	rec := serve(srv, "PUT", "/api/projects/"+escaped+"/visibility", bytes.NewBufferString(`{"mode":"hidden"}`))
	assert.Equal(t, 400, rec.Code)
	rec = serve(srv, "PUT", "/api/projects/missing/visibility", bytes.NewBufferString(`{"mode":"auto"}`))
	assert.Equal(t, 404, rec.Code)

	// Folder rule.
	rec = serve(srv, "PUT", "/api/visibility-folders", bytes.NewBufferString(`{"prefix":"org","mode":"private"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.True(t, isPrivate())
	rec = serve(srv, "GET", "/api/visibility-folders", nil)
	require.Equal(t, 200, rec.Code)
	var folders []db.VisibilityFolder
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &folders))
	require.Len(t, folders, 1)
	assert.Equal(t, "org", folders[0].Prefix)

	// Project override beats the folder.
	rec = serve(srv, "PUT", "/api/projects/"+escaped+"/visibility", bytes.NewBufferString(`{"mode":"public"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	assert.False(t, isPrivate())

	// The project page reports the inputs of the resolution.
	rec = serve(srv, "GET", "/api/projects/"+escaped, nil)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var page struct {
		Visibility db.ProjectVisibilityInfo `json:"visibility"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	assert.Equal(t, db.VisibilityPublic, page.Visibility.Mode)
	assert.Equal(t, "org", page.Visibility.FolderPrefix)

	// Release override.
	rec = serve(srv, "PUT", "/api/projects/"+escaped+"/releases/1/visibility", bytes.NewBufferString(`{"mode":"private"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	mode, err := d.ReleaseVisibility(ctx, rel.ID)
	require.NoError(t, err)
	assert.Equal(t, db.VisibilityPrivate, mode)
	rec = serve(srv, "PUT", "/api/projects/"+escaped+"/releases/9/visibility", bytes.NewBufferString(`{"mode":"private"}`))
	assert.Equal(t, 404, rec.Code)

	// Site override, keyed by a branch that holds a slash.
	rec = serve(srv, "PUT", "/api/projects/"+escaped+"/site-visibility", bytes.NewBufferString(`{"branch":"feat/x","mode":"public"}`))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	mode, err = d.SiteVisibility(ctx, p.ID, "feat/x")
	require.NoError(t, err)
	assert.Equal(t, db.VisibilityPublic, mode)
	rec = serve(srv, "PUT", "/api/projects/"+escaped+"/site-visibility", bytes.NewBufferString(`{"mode":"public"}`))
	assert.Equal(t, 400, rec.Code, "a site override names its branch")
}
