package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func runFileQuery(repo auth.OIDCRepoIdentity) string {
	return url.Values{"repository": {repo.RepoPath}, "run_id": {repo.RunID}}.Encode()
}

func putRunFile(t *testing.T, h *Handler, token, request auth.OIDCRepoIdentity, name string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/v1/run-files/"+name+"?"+runFileQuery(request), bytes.NewReader(body))
	req.SetPathValue("name", name)
	req = req.WithContext(runLockCtx(req.Context(), token))
	rec := httptest.NewRecorder()
	h.PutRunFile(rec, req)
	return rec
}

func getRunFile(t *testing.T, h *Handler, token auth.OIDCRepoIdentity, name string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/run-files/"+name+"?"+runFileQuery(token), nil)
	req.SetPathValue("name", name)
	for k, v := range header {
		req.Header[k] = v
	}
	req = req.WithContext(runLockCtx(req.Context(), token))
	rec := httptest.NewRecorder()
	h.GetRunFile(rec, req)
	return rec
}

func TestRunFile_RoundTrip(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	payload := bytes.Repeat([]byte("toolchain "), 4096)

	rec := putRunFile(t, h, testRunRepo, testRunRepo, "toolchain-Linux", payload)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var put runFileResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &put))
	assert.Equal(t, int64(len(payload)), put.Size)

	rec = getRunFile(t, h, testRunRepo, "toolchain-Linux", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, payload, rec.Body.Bytes())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, put.SHA256, rec.Header().Get("X-Content-SHA256"))
}

func TestRunFile_RangeResumes(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	payload := []byte("0123456789abcdef")
	require.Equal(t, http.StatusCreated, putRunFile(t, h, testRunRepo, testRunRepo, "part", payload).Code)

	rec := getRunFile(t, h, testRunRepo, "part", http.Header{"Range": {"bytes=10-"}})
	require.Equal(t, http.StatusPartialContent, rec.Code, rec.Body.String())
	assert.Equal(t, "abcdef", rec.Body.String())
}

func TestRunFile_ReRunAttemptReadsEarlierAttempt(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	require.Equal(t, http.StatusCreated, putRunFile(t, h, testRunRepo, testRunRepo, "ape", []byte("payload")).Code)

	rerun := testRunRepo
	rerun.RunAttempt = "2"
	rec := getRunFile(t, h, rerun, "ape", nil)
	require.Equal(t, http.StatusOK, rec.Code, "a re-run must read what the first attempt sent: %s", rec.Body.String())
	assert.Equal(t, "payload", rec.Body.String())
}

func TestRunFile_OtherRunCannotRead(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	require.Equal(t, http.StatusCreated, putRunFile(t, h, testRunRepo, testRunRepo, "ape", []byte("payload")).Code)

	other := testRunRepo
	other.RunID = "9999"
	rec := getRunFile(t, h, other, "ape", nil)
	require.Equal(t, http.StatusNotFound, rec.Code, "another run of the same repository holds nothing")

	rec = putRunFile(t, h, testRunRepo, other, "ape", []byte("x"))
	assert.Equal(t, http.StatusForbidden, rec.Code, "a request that names another run than its token is refused")
}

func TestRunFile_MissingNamesWhatTheRunHolds(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	require.Equal(t, http.StatusCreated, putRunFile(t, h, testRunRepo, testRunRepo, "toolchain-Linux", []byte("a")).Code)

	rec := getRunFile(t, h, testRunRepo, "toolchain-macOS", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "it holds: toolchain-Linux")
}

func TestRunFile_ReplaceFreesTheOldBlob(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	ctx := t.Context()
	rec := putRunFile(t, h, testRunRepo, testRunRepo, "ape", []byte("first"))
	require.Equal(t, http.StatusCreated, rec.Code)
	var first runFileResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))

	require.Equal(t, http.StatusCreated, putRunFile(t, h, testRunRepo, testRunRepo, "ape", []byte("second")).Code)
	exists, err := h.Store.Exists(ctx, first.SHA256)
	require.NoError(t, err)
	assert.False(t, exists, "a replaced run file's blob must be deleted when nothing else holds it")

	file, found, err := h.DB.GetRunFile(ctx, db.RunKey{RepoID: testRunRepo.RepoID, RunID: testRunRepo.RunID}, "ape")
	require.NoError(t, err)
	require.True(t, found)
	rc, _, err := h.Store.Get(ctx, file.StorageKey)
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "second", string(got))
}

func TestRunFile_InvalidName(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	rec := putRunFile(t, h, testRunRepo, testRunRepo, ".hidden", []byte("x"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
