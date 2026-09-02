package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPICreateToken(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPost, "/api/tokens", bytes.NewBufferString(`{"name":"mytoken","scopes":"read,write"}`))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"])
	details := resp["details"].(map[string]any)
	assert.Equal(t, "mytoken", details["name"])
	assert.Equal(t, "read,write", details["scopes"])
	assert.Equal(t, true, details["is_global"])
}

func TestAPICreateToken_ProjectScoped(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	ctx := context.Background()
	p, err := database.GetProject(ctx, "testproject")
	require.NoError(t, err)

	// Marshalled, not formatted: a quote or a backslash in a value would break a formatted document.
	spec, err := json.Marshal(map[string]any{"name": "proj-token", "scopes": "read", "project_id": p.ID})
	require.NoError(t, err)
	body := bytes.NewBuffer(spec)
	w := serve(srv, http.MethodPost, "/api/tokens", body)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	details := resp["details"].(map[string]any)
	assert.Equal(t, "proj-token", details["name"])
	assert.Equal(t, false, details["is_global"])
}

func TestAPICreateToken_MissingName(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPost, "/api/tokens", bytes.NewBufferString(`{"scopes":"read"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIUpdateToken(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	tokens, err := database.ListTokens(context.Background())
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	id := tokens[0].ID

	w := serve(srv, http.MethodPatch, "/api/tokens/"+fmt.Sprint(id), bytes.NewBufferString(`{"name":"renamed","scopes":"read"}`))
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAPIUpdateToken_InvalidID(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPatch, "/api/tokens/abc", bytes.NewBufferString(`{"name":"x","scopes":"read"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIDeleteToken(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	tokens, err := database.ListTokens(context.Background())
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	id := tokens[0].ID

	w := serve(srv, http.MethodDelete, "/api/tokens/"+fmt.Sprint(id), nil)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestAPIDeleteToken_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodDelete, "/api/tokens/9999", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIDeleteToken_InvalidID(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodDelete, "/api/tokens/abc", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPITokens(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "test-token", resp[0]["name"])
	assert.Equal(t, "read,write", resp[0]["scopes"])
	assert.Equal(t, true, resp[0]["is_global"])
	assert.NotNil(t, resp[0]["id"])
}

func TestAPITokens_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/tokens", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}
