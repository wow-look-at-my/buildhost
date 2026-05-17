package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/require"
)

func setupTestHandler(t *testing.T) *Handler {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })

	store, err := storage.NewFilesystem(t.TempDir())
	require.NoError(t, err)

	orch := repackage.NewOrchestrator(store, d, "http://localhost:8080")

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
