package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestAPIRunFiles(t *testing.T) {
	t.Serial()
	srv, database := newTestServer(t)
	srv.cfg.RunFilesKeep = 24 * time.Hour
	ctx := t.Context()

	live := db.RunKey{RepoID: "222", RunID: "1"}
	ended := db.RunKey{RepoID: "222", RunID: "2"}
	for _, key := range []db.RunKey{live, ended} {
		require.NoError(t, database.TouchRun(ctx, key, "myorg/compiler"))
		_, err := database.PutRunFile(ctx, key, "toolchain-Linux", db.RunBlob{StorageKey: "ab" + key.RunID, Size: 100})
		require.NoError(t, err)
	}
	endedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, database.MarkRunChecked(ctx, ended, &endedAt))

	w := serve(srv, http.MethodGet, "/api/run-files", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		KeepSeconds int64         `json:"keep_seconds"`
		TotalBytes  int64         `json:"total_bytes"`
		Runs        []runFilesRun `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(86400), resp.KeepSeconds)
	assert.Equal(t, int64(200), resp.TotalBytes)
	require.Len(t, resp.Runs, 2)

	byID := map[string]runFilesRun{}
	for _, r := range resp.Runs {
		byID[r.RunID] = r
	}
	assert.Nil(t, byID["1"].ExpiresAt, "a live run has no expiry")
	require.NotNil(t, byID["2"].ExpiresAt)
	assert.WithinDuration(t, endedAt.Add(24*time.Hour), *byID["2"].ExpiresAt, time.Second, "an ended run expires Keep after its end")
	require.Len(t, byID["1"].Files, 1)
	assert.Equal(t, "toolchain-Linux", byID["1"].Files[0].Name)
	assert.Equal(t, "myorg/compiler", byID["1"].Repo)
}
