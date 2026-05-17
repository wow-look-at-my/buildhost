package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestCreateProject_Success(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"myproject","description":"A test project","versioning":"semver"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var p model.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "myproject", p.Name)
	assert.Equal(t, model.VersioningSemver, p.Versioning)
}

func TestCreateProject_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"myproject"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateProject_EmptyName(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":""}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "name is required")
}

func TestCreateProject_InvalidBody(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader("not json"))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateProject_InvalidVersioning(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"myproject","versioning":"bogus"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "versioning must be")
}

func TestCreateProject_DefaultVersioning(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"autoproject"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var p model.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, model.VersioningAuto, p.Versioning)
}

func TestCreateProject_Duplicate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "dup", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"name":"dup"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestGetProject_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "testproj", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/projects/testproj", nil)
	req.SetPathValue("project", "testproj")
	rec := httptest.NewRecorder()
	h.GetProject(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var p model.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "testproj", p.Name)
}

func TestGetProject_NotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/projects/missing", nil)
	req.SetPathValue("project", "missing")
	rec := httptest.NewRecorder()
	h.GetProject(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetProject_PrivateWithoutAuth(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "secret", IsPrivate: true, Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/projects/secret", nil)
	req.SetPathValue("project", "secret")
	rec := httptest.NewRecorder()
	h.GetProject(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListProjects_FiltersPrivate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	require.NoError(t, h.DB.CreateProject(ctx, &model.Project{Name: "public1", Versioning: model.VersioningAuto}))
	require.NoError(t, h.DB.CreateProject(ctx, &model.Project{Name: "private1", IsPrivate: true, Versioning: model.VersioningAuto}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	rec := httptest.NewRecorder()
	h.ListProjects(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var projects []model.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))
	assert.Equal(t, 1, len(projects))
	assert.Equal(t, "public1", projects[0].Name)
}

func TestListProjects_AuthSeesPrivate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	require.NoError(t, h.DB.CreateProject(ctx, &model.Project{Name: "pub", Versioning: model.VersioningAuto}))
	require.NoError(t, h.DB.CreateProject(ctx, &model.Project{Name: "priv", IsPrivate: true, Versioning: model.VersioningAuto}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.ListProjects(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var projects []model.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))
	assert.Equal(t, 2, len(projects))
}
