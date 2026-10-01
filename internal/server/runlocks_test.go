package server_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/server"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// A scheduled run's token fails the event allowlist. It may still claim and
// read its own run's locks, and nothing else.
func TestRunLock_ScheduleEventTokenOnlyReachesRunLocks(t *testing.T) {
	t.Serial()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	database, err := db.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwksSrv := jwksServer(t, &key.PublicKey, "kid-runlock")

	srv := server.New(config.Config{
		ListenAddr:  ":0",
		DataDir:     dbDir,
		DBPath:      dbPath,
		OIDCIssuers: []string{jwksSrv.URL},
		OIDCOrgs:    []string{"*"},
		OIDCEvents:  []string{"push", "pull_request", "workflow_dispatch"},
	}, database, store)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	token := signJWT(t, key, "kid-runlock", map[string]any{
		"iss":           jwksSrv.URL,
		"sub":           "repo:myorg/consumer:ref:refs/heads/main",
		"repository":    "myorg/consumer",
		"repository_id": "222",
		"run_id":        "4242",
		"run_attempt":   "1",
		"event_name":    "schedule",
		"aud":           ts.URL,
		"exp":           time.Now().Add(10 * time.Minute).Unix(),
		"iat":           time.Now().Unix(),
	})
	do := func(method, path, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(data)
	}

	claim := jsonDoc(t, map[string]any{
		"repository": "myorg/consumer", "run_id": "4242", "run_attempt": "1",
		"name": "github.com/wow-look-at-my/alpha@HEAD", "value": "v0.0.0-a",
	})
	status, body := do("POST", "/api/v1/run-locks", claim)
	require.Equal(t, http.StatusOK, status, body)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	assert.Equal(t, "v0.0.0-a", got["value"])
	assert.Equal(t, true, got["created"])

	status, body = do("GET", "/api/v1/run-locks?repository=myorg/consumer&run_id=4242&run_attempt=1&name=github.com/wow-look-at-my/alpha@HEAD", "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"value":"v0.0.0-a"`)

	// The run binding still holds for a schedule token.
	status, _ = do("GET", "/api/v1/run-locks?repository=myorg/consumer&run_id=1&run_attempt=1&name=x", "")
	assert.Equal(t, http.StatusForbidden, status)

	// Every other endpoint keeps the allowlist: no release, no project.
	status, body = do("POST", "/api/v1/projects/consumer/releases", `{}`)
	assert.Equal(t, http.StatusUnauthorized, status, body)
	status, body = do("PUT", "/api/v1/projects/consumer/releases/latest/artifacts/linux/amd64", "binary")
	assert.Equal(t, http.StatusUnauthorized, status, body)
	_, err = database.GetProject(t.Context(), "consumer")
	assert.ErrorIs(t, err, db.ErrNotFound, "a schedule token must not provision a project")
}
