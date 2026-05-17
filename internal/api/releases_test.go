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

func TestCreateRelease_Semver(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "semproj", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"version":"v1.2.3","git_branch":"main","git_commit":"abc123"}`
	req := httptest.NewRequest("POST", "/api/projects/semproj/releases", strings.NewReader(body))
	req.SetPathValue("project", "semproj")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel model.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "1.2.3", rel.Version)
	assert.Equal(t, "main", rel.GitBranch)
}

func TestCreateRelease_Auto(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "autoproj", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{}`
	req := httptest.NewRequest("POST", "/api/projects/autoproj/releases", strings.NewReader(body))
	req.SetPathValue("project", "autoproj")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel model.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "1", rel.Version)
	assert.Equal(t, int64(1), rel.VersionNum)
}

func TestCreateRelease_AutoWithExplicitVersion(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "autov", Versioning: model.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"version":"5"}`
	req := httptest.NewRequest("POST", "/api/projects/autov/releases", strings.NewReader(body))
	req.SetPathValue("project", "autov")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel model.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "5", rel.Version)
	assert.Equal(t, int64(5), rel.VersionNum)
}

func TestCreateRelease_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"version":"1.0.0"}`
	req := httptest.NewRequest("POST", "/api/projects/proj/releases", strings.NewReader(body))
	req.SetPathValue("project", "proj")
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateRelease_ProjectNotFound(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"version":"1.0.0"}`
	req := httptest.NewRequest("POST", "/api/projects/missing/releases", strings.NewReader(body))
	req.SetPathValue("project", "missing")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreateRelease_InvalidBody(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "badbody", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("POST", "/api/projects/badbody/releases", strings.NewReader("not json"))
	req.SetPathValue("project", "badbody")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateRelease_SemverMissingVersion(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "semproj2", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{}`
	req := httptest.NewRequest("POST", "/api/projects/semproj2/releases", strings.NewReader(body))
	req.SetPathValue("project", "semproj2")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "version is required")
}

func TestCreateRelease_Duplicate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "duprel", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	body := `{"version":"1.0.0"}`
	req := httptest.NewRequest("POST", "/api/projects/duprel/releases", strings.NewReader(body))
	req.SetPathValue("project", "duprel")
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestGetRelease_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "relproj", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "2.0.0", VersionNum: 2000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("GET", "/api/projects/relproj/releases/2.0.0", nil)
	req.SetPathValue("project", "relproj")
	req.SetPathValue("version", "2.0.0")
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got model.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "2.0.0", got.Version)
}

func TestGetRelease_ProjectNotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/projects/missing/releases/1.0.0", nil)
	req.SetPathValue("project", "missing")
	req.SetPathValue("version", "1.0.0")
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetRelease_ReleaseNotFound(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "relproj2", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/projects/relproj2/releases/9.9.9", nil)
	req.SetPathValue("project", "relproj2")
	req.SetPathValue("version", "9.9.9")
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetRelease_PrivateProjectNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "privget", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("GET", "/api/projects/privget/releases/1.0.0", nil)
	req.SetPathValue("project", "privget")
	req.SetPathValue("version", "1.0.0")
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListReleases_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "listrel", Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	require.NoError(t, h.DB.CreateRelease(ctx, &model.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}))
	require.NoError(t, h.DB.CreateRelease(ctx, &model.Release{ProjectID: proj.ID, Version: "2.0.0", VersionNum: 2000000}))

	req := httptest.NewRequest("GET", "/api/projects/listrel/releases", nil)
	req.SetPathValue("project", "listrel")
	rec := httptest.NewRecorder()
	h.ListReleases(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var releases []model.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &releases))
	assert.Equal(t, 2, len(releases))
}

func TestListReleases_ProjectNotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/projects/missing/releases", nil)
	req.SetPathValue("project", "missing")
	rec := httptest.NewRecorder()
	h.ListReleases(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListReleases_PrivateProjectNoAuth(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &model.Project{Name: "privrel", IsPrivate: true, Versioning: model.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/projects/privrel/releases", nil)
	req.SetPathValue("project", "privrel")
	rec := httptest.NewRecorder()
	h.ListReleases(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSemverToNum(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"1.0.0", 1_000_000},
		{"1.2.3", 1_002_003},
		{"0.1.0", 1_000},
		{"0.0.1", 1},
		{"2.10.5", 2_010_005},
		{"1.0.0-rc1", 1_000_000},
	}

	for _, tt := range tests {
		got := semverToNum(tt.input)
		assert.Equal(t, tt.expected, got, "semverToNum(%q)", tt.input)
	}
}
