package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestCreateToken_Success(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"ci-token","scopes":"read,write"}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"])
	assert.NotNil(t, resp["details"])
}

func TestCreateToken_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"ci-token"}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateToken_EmptyName(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":""}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "name is required")
}

func TestCreateToken_InvalidBody(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader("not json"))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateToken_DefaultScopes(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"default-scope-token"}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestCreateToken_WithProjectID(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "scoped", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"name":"project-token","project_id":` + strconv.FormatInt(proj.ID, 10) + `,"scopes":"read"}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestListTokens_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	_, _, err := h.DB.CreateToken(ctx, "tok1", nil, "read")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/tokens", nil)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.ListTokens(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var tokens []model.APIToken
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tokens))
	assert.GreaterOrEqual(t, len(tokens), 1)
}

func TestListTokens_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/tokens", nil)
	rec := httptest.NewRecorder()
	h.ListTokens(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeleteToken_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	_, tok, err := h.DB.CreateToken(ctx, "del-me", nil, "read")
	require.NoError(t, err)

	req := httptest.NewRequest("DELETE", "/api/tokens/"+strconv.FormatInt(tok.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(tok.ID, 10))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.DeleteToken(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDeleteToken_NotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("DELETE", "/api/tokens/9999", nil)
	req.SetPathValue("id", "9999")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.DeleteToken(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteToken_InvalidID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("DELETE", "/api/tokens/abc", nil)
	req.SetPathValue("id", "abc")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.DeleteToken(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid token id")
}

func TestDeleteToken_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("DELETE", "/api/tokens/1", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	h.DeleteToken(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// --- Security tests: project-scoped token isolation ---

func TestCreateToken_ProjectScopedCannotCreateGlobalToken(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "proj-a", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	// Request body has no project_id, meaning it would create a global token
	body := `{"name":"escalate-token","scopes":"read,write"}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(projectWriteToken(req.Context(), proj.ID))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateToken_ProjectScopedCannotCreateTokenForDifferentProject(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	projA := &model.Project{Name: "proj-scope-a", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, projA))
	projB := &model.Project{Name: "proj-scope-b", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, projB))

	// Token is scoped to project A, but tries to create a token for project B
	body := `{"name":"cross-project","scopes":"read","project_id":` + strconv.FormatInt(projB.ID, 10) + `}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(projectWriteToken(req.Context(), projA.ID))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestListTokens_ProjectScopedCannotList(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "proj-list", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/tokens", nil)
	req = req.WithContext(projectWriteToken(req.Context(), proj.ID))
	rec := httptest.NewRecorder()
	h.ListTokens(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDeleteToken_ProjectScopedCannotDelete(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "proj-del", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	// Create a token that the project-scoped token will try to delete
	_, tok, err := h.DB.CreateToken(ctx, "victim", nil, "read")
	require.NoError(t, err)

	req := httptest.NewRequest("DELETE", "/api/tokens/"+strconv.FormatInt(tok.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(tok.ID, 10))
	req = req.WithContext(projectWriteToken(req.Context(), proj.ID))
	rec := httptest.NewRecorder()
	h.DeleteToken(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateToken_InvalidScopeRejected(t *testing.T) {
	h := setupTestHandler(t)

	tests := []struct {
		name   string
		scopes string
	}{
		{"unknown scope", `{"name":"bad","scopes":"admin"}`},
		{"partial invalid", `{"name":"bad","scopes":"read,admin"}`},
		{"empty scope element", `{"name":"bad","scopes":"read,,write"}`},
		{"injection attempt", `{"name":"bad","scopes":"read,write,delete"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(tc.scopes))
			req = req.WithContext(writeToken(req.Context(), "read,write"))
			rec := httptest.NewRecorder()
			h.CreateToken(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "invalid scope")
		})
	}
}

func TestCreateToken_GlobalTokenCanCreateProjectScoped(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "proj-global-create", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"name":"project-token","scopes":"read,write","project_id":` + strconv.FormatInt(proj.ID, 10) + `}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"])
}

func TestCreateToken_ProjectScopedCanCreateForSameProject(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "proj-same", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	// Project-scoped token CAN create a token for the same project
	body := `{"name":"same-project-token","scopes":"read","project_id":` + strconv.FormatInt(proj.ID, 10) + `}`
	req := httptest.NewRequest("POST", "/api/tokens", strings.NewReader(body))
	req = req.WithContext(projectWriteToken(req.Context(), proj.ID))
	rec := httptest.NewRecorder()
	h.CreateToken(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}
