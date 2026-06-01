package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
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

	var p db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "myproject", p.Name)
	assert.Equal(t, db.VersioningSemver, p.Versioning)
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

	var p db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, db.VersioningAuto, p.Versioning)
}

func TestCreateProject_Duplicate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "dup", Versioning: db.VersioningAuto}
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

	proj := &db.Project{Name: "testproj", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/projects/testproj", nil)
	req.SetPathValue("project", "testproj")
	req = withProjectRoute(req, proj)
	rec := httptest.NewRecorder()
	h.GetProject(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var p db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "testproj", p.Name)
}

// Note: GetProject auth (private project, not found) is tested via requireProject
// middleware in the auth package.

func TestListProjects_FiltersPrivate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	require.NoError(t, h.DB.CreateProject(ctx, &db.Project{Name: "public1", Versioning: db.VersioningAuto}))
	require.NoError(t, h.DB.CreateProject(ctx, &db.Project{Name: "private1", IsPrivate: true, Versioning: db.VersioningAuto}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	rec := httptest.NewRecorder()
	h.ListProjects(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var projects []db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))
	assert.Equal(t, 1, len(projects))
	assert.Equal(t, "public1", projects[0].Name)
}

func TestListProjects_AuthSeesPrivate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	require.NoError(t, h.DB.CreateProject(ctx, &db.Project{Name: "pub", Versioning: db.VersioningAuto}))
	require.NoError(t, h.DB.CreateProject(ctx, &db.Project{Name: "priv", IsPrivate: true, Versioning: db.VersioningAuto}))

	req := httptest.NewRequest("GET", "/api/projects", nil)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.ListProjects(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var projects []db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))
	assert.Equal(t, 2, len(projects))
}

func TestValidProjectName_SlashNamespaced(t *testing.T) {
	for _, name := range []string{"log-streamer", "log-streamer/client", "log-streamer/server", "foo/cli", "a/b/c", "x"} {
		assert.True(t, validProjectName(name), "expected valid: %q", name)
	}
	for _, name := range []string{"", "/foo", "foo/", "foo//bar", "Foo", "foo/Bar", "-foo", "/", "foo bar"} {
		assert.False(t, validProjectName(name), "expected invalid: %q", name)
	}
}

func TestCreateProject_SlashNamespaced(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"name":"log-streamer/client","versioning":"auto"}`
	req := httptest.NewRequest("POST", "/api/projects", strings.NewReader(body))
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateProject(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var p db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "log-streamer/client", p.Name)
}
