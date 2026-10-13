package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
)

var testRunRepo = auth.OIDCRepoIdentity{
	RepoPath:   "wow-look-at-my/consumer",
	Issuer:     auth.GitHubActionsIssuer,
	OwnerID:    "111",
	RepoID:     "222",
	RunID:      "4242",
	RunAttempt: "1",
}

// runLockCtx is what the auth middleware attaches for a verified job token.
func runLockCtx(ctx context.Context, repo auth.OIDCRepoIdentity) context.Context {
	return auth.WithOIDCRepo(writeToken(ctx, "read,write"), repo)
}

func claimRunLock(t *testing.T, h *Handler, repo auth.OIDCRepoIdentity, body map[string]any) (*httptest.ResponseRecorder, runLockResponse) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/run-locks", strings.NewReader(jsonDoc(t, body)))
	req = req.WithContext(runLockCtx(req.Context(), repo))
	rec := httptest.NewRecorder()
	h.ClaimRunLock(rec, req)
	var resp runLockResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec, resp
}

func getRunLock(t *testing.T, h *Handler, repo auth.OIDCRepoIdentity, name string) (*httptest.ResponseRecorder, runLockResponse) {
	t.Helper()
	q := url.Values{"repository": {repo.RepoPath}, "run_id": {repo.RunID}, "run_attempt": {repo.RunAttempt}, "name": {name}}
	req := httptest.NewRequest("GET", "/api/v1/run-locks?"+q.Encode(), nil)
	req = req.WithContext(runLockCtx(req.Context(), repo))
	rec := httptest.NewRecorder()
	h.GetRunLock(rec, req)
	var resp runLockResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec, resp
}

func lockBody(repo auth.OIDCRepoIdentity, name, value string) map[string]any {
	return map[string]any{
		"repository":  repo.RepoPath,
		"run_id":      repo.RunID,
		"run_attempt": repo.RunAttempt,
		"name":        name,
		"value":       value,
	}
}

const lockName = "github.com/wow-look-at-my/alpha@HEAD"

func TestRunLock_FirstClaimWins(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)

	rec, got := getRunLock(t, h, testRunRepo, lockName)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, got.Found, "an unclaimed lock must read as absent")

	rec, got = claimRunLock(t, h, testRunRepo, lockBody(testRunRepo, lockName, "v0.0.0-a"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, runLockResponse{Found: true, Value: "v0.0.0-a", Created: true}, got)

	rec, got = claimRunLock(t, h, testRunRepo, lockBody(testRunRepo, lockName, "v0.0.0-b"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, runLockResponse{Found: true, Value: "v0.0.0-a"}, got, "a second claim must get the first value back")

	rec, got = getRunLock(t, h, testRunRepo, lockName)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, runLockResponse{Found: true, Value: "v0.0.0-a"}, got)
}

func TestRunLock_AttemptsAndRunsAreSeparate(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	_, _ = claimRunLock(t, h, testRunRepo, lockBody(testRunRepo, lockName, "v0.0.0-a"))

	retry := testRunRepo
	retry.RunAttempt = "2"
	_, got := claimRunLock(t, h, retry, lockBody(retry, lockName, "v0.0.0-b"))
	assert.Equal(t, "v0.0.0-b", got.Value, "a new attempt must lock again")

	other := testRunRepo
	other.RepoID, other.RepoPath = "333", "wow-look-at-my/other"
	_, got = getRunLock(t, h, other, lockName)
	assert.False(t, got.Found, "another repository must not see this run's locks")
}

func TestRunLock_RacingClaimsConverge(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	const claims = 16
	values := make([]string, claims)
	created := make([]bool, claims)
	var wg sync.WaitGroup
	for i := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/v1/run-locks", strings.NewReader(jsonDoc(t, lockBody(testRunRepo, lockName, fmt.Sprintf("v0.0.0-%d", i)))))
			req = req.WithContext(runLockCtx(req.Context(), testRunRepo))
			rec := httptest.NewRecorder()
			h.ClaimRunLock(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var resp runLockResponse
			assert.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			values[i], created[i] = resp.Value, resp.Created
		}()
	}
	wg.Wait()
	winners := 0
	for i := range claims {
		assert.Equal(t, values[0], values[i], "every claim must return the winner")
		if created[i] {
			winners++
		}
	}
	assert.Equal(t, 1, winners, "exactly one claim must write")
}

func TestRunLock_Refusals(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)

	// A static token names no run.
	req := httptest.NewRequest("POST", "/api/v1/run-locks", strings.NewReader(jsonDoc(t, lockBody(testRunRepo, lockName, "v"))))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.ClaimRunLock(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "run_id")

	// No token at all.
	req = httptest.NewRequest("GET", "/api/v1/run-locks?name=x", nil)
	rec = httptest.NewRecorder()
	h.GetRunLock(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// A request for another run than the token's.
	other := testRunRepo
	other.RunID = "1"
	rec, _ = claimRunLock(t, h, testRunRepo, lockBody(other, lockName, "v"))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "#1.1")
	rec, _ = claimRunLock(t, h, testRunRepo, lockBody(auth.OIDCRepoIdentity{RepoPath: "someone/else", RunID: "4242", RunAttempt: "1"}, lockName, "v"))
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// A name and a value are required.
	rec, _ = claimRunLock(t, h, testRunRepo, lockBody(testRunRepo, "", "v"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	rec, _ = claimRunLock(t, h, testRunRepo, lockBody(testRunRepo, lockName, ""))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	_, got := getRunLock(t, h, testRunRepo, lockName)
	assert.False(t, got.Found, "a refused claim must record nothing")
}
