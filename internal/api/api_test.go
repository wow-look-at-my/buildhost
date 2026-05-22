package api

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/require"
)

// withRoute adds project and route info to the request context, simulating
// what the auth middleware does in production.
func withRoute(r *http.Request, project *model.Project, rt route) *http.Request {
	ctx := auth.WithProject(r.Context(), project)
	ctx = auth.WithRouteInfo(ctx, rt)
	return r.WithContext(ctx)
}

// withProjectRoute adds project and route info derived from the request's path values.
func withProjectRoute(r *http.Request, project *model.Project) *http.Request {
	rt := route{
		project: r.PathValue("project"),
		version: r.PathValue("version"),
		os:      r.PathValue("os"),
		arch:    r.PathValue("arch"),
		write:   r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE",
	}
	return withRoute(r, project, rt)
}

func setupTestHandler(t *testing.T) *Handler {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir())
	require.NoError(t, err)

	orch := repackage.NewOrchestrator(store, d, t.TempDir())

	return &Handler{DB: d, Store: store, Orchestrator: orch}
}

func writeToken(ctx context.Context, scopes string) context.Context {
	tok := &model.APIToken{ID: 99999, Scopes: scopes}
	return auth.WithToken(ctx, tok)
}

func projectWriteToken(ctx context.Context, projectID int64) context.Context {
	tok := &model.APIToken{ID: 99999, Scopes: "read,write", ProjectID: &projectID}
	return auth.WithToken(ctx, tok)
}
