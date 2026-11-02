package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/runfiles"
)

func TestAPIRunFiles(t *testing.T) {
	t.Serial()
	srv, database := newTestServer(t)
	srv.cfg.RunFilesKeep = 24 * time.Hour
	ctx := t.Context()

	live := db.RunKey{RepoID: "222", RunID: "1"}
	ended := db.RunKey{RepoID: "222", RunID: "2"}
	unreadable := db.RunKey{RepoID: "222", RunID: "3"}
	for _, key := range []db.RunKey{live, ended, unreadable} {
		require.NoError(t, database.TouchRun(ctx, key, "myorg/compiler"))
		_, err := database.PutRunFile(ctx, key, "toolchain-Linux", db.RunBlob{StorageKey: "ab" + key.RunID, Size: 100})
		require.NoError(t, err)
	}
	endedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	require.NoError(t, database.MarkRunChecked(ctx, ended, &endedAt))
	j := &runfiles.Janitor{DB: database, Keep: 24 * time.Hour, RunEnded: func(_ context.Context, _, runID string) (time.Time, bool, error) {
		if runID == unreadable.RunID {
			return time.Time{}, false, errors.New("GitHub answered 401")
		}
		return time.Time{}, false, nil
	}}
	require.NoError(t, j.Sweep(ctx, time.Now()))

	w := serve(srv, http.MethodGet, "/api/run-files", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		KeepSeconds int64         `json:"keep_seconds"`
		TotalBytes  int64         `json:"total_bytes"`
		Runs        []runFilesRun `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(86400), resp.KeepSeconds)
	assert.Equal(t, int64(300), resp.TotalBytes)
	require.Len(t, resp.Runs, 3)

	byID := map[string]runFilesRun{}
	for _, r := range resp.Runs {
		byID[r.RunID] = r
	}
	assert.Equal(t, "live", byID["1"].State)
	assert.Equal(t, "ended", byID["2"].State)
	assert.Equal(t, "unknown", byID["3"].State, "a run GitHub cannot report on must not read as live")
	assert.Equal(t, "GitHub answered 401", byID["3"].CheckError)
	require.NotNil(t, byID["3"].ExpiresAt, "the janitor deletes an unknown run Keep after its last use")
	assert.WithinDuration(t, byID["3"].UsedAt.Add(24*time.Hour), *byID["3"].ExpiresAt, time.Second)
	assert.Nil(t, byID["1"].ExpiresAt, "a live run has no expiry")
	require.NotNil(t, byID["2"].ExpiresAt)
	assert.WithinDuration(t, endedAt.Add(24*time.Hour), *byID["2"].ExpiresAt, time.Second, "an ended run expires Keep after its end")
	require.Len(t, byID["1"].Files, 1)
	assert.Equal(t, "toolchain-Linux", byID["1"].Files[0].Name)
	assert.Equal(t, "myorg/compiler", byID["1"].Repo)
}
