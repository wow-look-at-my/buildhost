package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestGitHubWebhook_DeleteBranchDeletesRepoNamespaceSites(t *testing.T) {
	h := setupTestHandler(t)
	h.GitHubWebhookSecret = "secret"
	ctx := context.Background()

	root := seedWebhookProject(t, h.DB, "myrepo")
	nested := seedWebhookProject(t, h.DB, "myrepo/docs")
	other := seedWebhookProject(t, h.DB, "other")

	rootKey := putWebhookBlob(t, h, "root")
	nestedKey := putWebhookBlob(t, h, "nested")
	otherKey := putWebhookBlob(t, h, "other")
	mainKey := putWebhookBlob(t, h, "main")

	upsertWebhookSite(t, h.DB, root.ID, "feature-x", rootKey)
	upsertWebhookSite(t, h.DB, nested.ID, "feature-x", nestedKey)
	upsertWebhookSite(t, h.DB, other.ID, "feature-x", otherKey)
	upsertWebhookSite(t, h.DB, root.ID, "main", mainKey)

	payload := []byte(`{"ref":"feature-x","ref_type":"branch","repository":{"name":"MyRepo"}}`)
	req := signedGitHubWebhookRequest(t, h.GitHubWebhookSecret, payload)
	rec := httptest.NewRecorder()
	h.GitHubWebhook(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, float64(2), resp["sites_deleted"])
	require.Equal(t, float64(2), resp["blobs_deleted"])

	_, err := h.DB.GetSite(ctx, root.ID, "feature-x")
	require.True(t, errors.Is(err, db.ErrNotFound))
	_, err = h.DB.GetSite(ctx, nested.ID, "feature-x")
	require.True(t, errors.Is(err, db.ErrNotFound))
	_, err = h.DB.GetSite(ctx, root.ID, "main")
	require.NoError(t, err)
	_, err = h.DB.GetSite(ctx, other.ID, "feature-x")
	require.NoError(t, err)
}

func TestGitHubWebhook_InvalidSignatureDoesNotDelete(t *testing.T) {
	h := setupTestHandler(t)
	h.GitHubWebhookSecret = "secret"
	project := seedWebhookProject(t, h.DB, "myrepo")
	key := putWebhookBlob(t, h, "root")
	upsertWebhookSite(t, h.DB, project.ID, "feature-x", key)

	payload := []byte(`{"ref":"feature-x","ref_type":"branch","repository":{"name":"myrepo"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "delete")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec := httptest.NewRecorder()
	h.GitHubWebhook(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	_, err := h.DB.GetSite(context.Background(), project.ID, "feature-x")
	require.NoError(t, err)
}

func TestGitHubWebhook_IgnoresTagDelete(t *testing.T) {
	h := setupTestHandler(t)
	h.GitHubWebhookSecret = "secret"
	project := seedWebhookProject(t, h.DB, "myrepo")
	key := putWebhookBlob(t, h, "root")
	upsertWebhookSite(t, h.DB, project.ID, "v1.0.0", key)

	payload := []byte(`{"ref":"v1.0.0","ref_type":"tag","repository":{"name":"myrepo"}}`)
	req := signedGitHubWebhookRequest(t, h.GitHubWebhookSecret, payload)
	rec := httptest.NewRecorder()
	h.GitHubWebhook(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	_, err := h.DB.GetSite(context.Background(), project.ID, "v1.0.0")
	require.NoError(t, err)
}

func TestGitHubWebhook_NotConfigured(t *testing.T) {
	h := setupTestHandler(t)
	payload := []byte(`{"zen":"Keep it logically awesome."}`)
	req := signedGitHubWebhookRequest(t, "secret", payload)
	rec := httptest.NewRecorder()
	h.GitHubWebhook(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func signedGitHubWebhookRequest(t *testing.T, secret string, payload []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "delete")
	req.Header.Set("X-Hub-Signature-256", signGitHubWebhook(secret, payload))
	return req
}

func signGitHubWebhook(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func seedWebhookProject(t *testing.T, d *db.DB, name string) *db.Project {
	t.Helper()
	p := &db.Project{Name: name, Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(context.Background(), p))
	return p
}

func putWebhookBlob(t *testing.T, h *Handler, body string) string {
	t.Helper()
	key, _, err := h.Store.Put(context.Background(), bytes.NewBufferString(body))
	require.NoError(t, err)
	return key
}

func upsertWebhookSite(t *testing.T, d *db.DB, projectID int64, branch, key string) {
	t.Helper()
	_, err := d.UpsertSite(context.Background(), &db.Site{
		ProjectID:  projectID,
		Branch:     branch,
		StorageKey: key,
		Size:       int64(len(key)),
		SHA256:     key,
		FileCount:  1,
	})
	require.NoError(t, err)
}
