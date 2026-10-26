package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

type stubRefs struct{ present map[string]bool }

func (stubRefs) DefaultBranch(_ context.Context, repo string) (string, error) {
	if repo != "o/app" {
		return "", errors.New("GitHub answered 404 for repository " + repo)
	}
	return "main", nil
}

func (s stubRefs) RefPresent(_ context.Context, _, name string) (bool, error) {
	return s.present[name], nil
}

func TestAdminRetention_DeletedBranches(t *testing.T) {
	t.Serial()
	srv, database := newTestServer(t)
	ctx := context.Background()

	prev := refChecker
	refChecker = stubRefs{present: map[string]bool{"live": true}}
	t.Cleanup(func() { refChecker = prev })

	p := &db.Project{Name: "app", Versioning: db.VersioningAuto, GithubRepo: "o/app"}
	require.NoError(t, database.CreateProject(ctx, p))
	require.NoError(t, database.SetProjectDefaultBranch(ctx, p.ID, "main"))
	for i, branch := range []string{"main", "live", "gone"} {
		r := &db.Release{ProjectID: p.ID, Version: branch, VersionNum: int64(i + 1), GitBranch: branch}
		require.NoError(t, database.CreateRelease(ctx, r))
		require.NoError(t, database.PublishRelease(ctx, r.ID))
	}
	_, err := database.DB.ExecContext(ctx, "UPDATE releases SET created_at = datetime('now', '-2 days')")
	require.NoError(t, err)

	rec := serve(srv, "GET", "/api/retention/deleted-branches", nil)
	require.Equal(t, 200, rec.Code)
	var plan map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &plan))
	assert.Equal(t, false, plan["enforced"])
	assert.Equal(t, float64(1), plan["release_count"])
	require.Len(t, plan["branches"], 2)
	_, err = database.GetRelease(ctx, p.ID, "gone")
	require.NoError(t, err, "the preview deletes nothing")

	atomic.AddInt64(&inflightWrites, 1)
	rec = serve(srv, "POST", "/api/retention/deleted-branches", nil)
	atomic.AddInt64(&inflightWrites, -1)
	assert.Equal(t, http.StatusConflict, rec.Code)

	rec = serve(srv, "POST", "/api/retention/deleted-branches", nil)
	require.Equal(t, 200, rec.Code)
	var rep map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep))
	assert.Equal(t, true, rep["enforced"])
	releases := rep["releases"].([]any)
	require.Len(t, releases, 1)
	assert.Equal(t, "deleted-branch", releases[0].(map[string]any)["reason"])

	_, err = database.GetRelease(ctx, p.ID, "gone")
	assert.ErrorIs(t, err, db.ErrNotFound)
	for _, v := range []string{"main", "live"} {
		_, err = database.GetRelease(ctx, p.ID, v)
		assert.NoError(t, err, "%s must stay", v)
	}
}
