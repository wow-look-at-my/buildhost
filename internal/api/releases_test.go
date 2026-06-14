package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestCreateRelease_Semver(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "semproj", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"version":"v1.2.3","git_branch":"main","git_commit":"abc123"}`
	req := httptest.NewRequest("POST", "/api/projects/semproj/releases", strings.NewReader(body))
	req.SetPathValue("project", "semproj")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "1.2.3", rel.Version)
	assert.Equal(t, "main", rel.GitBranch)
}

func TestCreateRelease_Auto(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "autoproj", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{}`
	req := httptest.NewRequest("POST", "/api/projects/autoproj/releases", strings.NewReader(body))
	req.SetPathValue("project", "autoproj")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "1", rel.Version)
	assert.Equal(t, int64(1), rel.VersionNum)
}

func TestCreateRelease_AutoWithExplicitVersion(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "autov", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"version":"5"}`
	req := httptest.NewRequest("POST", "/api/projects/autov/releases", strings.NewReader(body))
	req.SetPathValue("project", "autov")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "5", rel.Version)
	assert.Equal(t, int64(5), rel.VersionNum)
}

// Note: TestCreateRelease_NoAuth removed -- auth is now enforced by the
// requireProject middleware (tested in the auth package).

func TestCreateRelease_InvalidBody(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "badbody", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("POST", "/api/projects/badbody/releases", strings.NewReader("not json"))
	req.SetPathValue("project", "badbody")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateRelease_SemverMissingVersion(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "semproj2", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{}`
	req := httptest.NewRequest("POST", "/api/projects/semproj2/releases", strings.NewReader(body))
	req.SetPathValue("project", "semproj2")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "version is required")
}

func TestCreateRelease_Duplicate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "duprel", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	rel := &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	body := `{"version":"1.0.0"}`
	req := httptest.NewRequest("POST", "/api/projects/duprel/releases", strings.NewReader(body))
	req.SetPathValue("project", "duprel")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestGetRelease_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "relproj", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "2.0.0", VersionNum: 2000000}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))

	req := httptest.NewRequest("GET", "/api/projects/relproj/releases/2.0.0", nil)
	req.SetPathValue("project", "relproj")
	req.SetPathValue("version", "2.0.0")
	req = withProjectRoute(req, proj)
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "2.0.0", got.Version)
}

func TestGetRelease_ReleaseNotFound(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "relproj2", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	req := httptest.NewRequest("GET", "/api/projects/relproj2/releases/9.9.9", nil)
	req.SetPathValue("project", "relproj2")
	req.SetPathValue("version", "9.9.9")
	req = withProjectRoute(req, proj)
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetRelease_Latest(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "latestproj", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	// Two published releases; "latest" must resolve to the highest version.
	older := &db.Release{ProjectID: proj.ID, Version: "1", VersionNum: 1, GitBranch: "master", GitCommit: "aaa111"}
	require.NoError(t, h.DB.CreateRelease(ctx, older))
	require.NoError(t, h.DB.PublishRelease(ctx, older.ID))
	newer := &db.Release{ProjectID: proj.ID, Version: "2", VersionNum: 2, GitBranch: "master", GitCommit: "bbb222"}
	require.NoError(t, h.DB.CreateRelease(ctx, newer))
	require.NoError(t, h.DB.PublishRelease(ctx, newer.ID))

	req := httptest.NewRequest("GET", "/api/projects/latestproj/releases/latest", nil)
	req.SetPathValue("project", "latestproj")
	req.SetPathValue("version", "latest")
	req = withProjectRoute(req, proj)
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "2", got.Version)
	assert.Equal(t, "bbb222", got.GitCommit)
	assert.True(t, got.Published)
}

func TestGetRelease_LatestNoPublishedReleases(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "latestempty", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	// An unpublished release does not count as "latest".
	unpub := &db.Release{ProjectID: proj.ID, Version: "1", VersionNum: 1, GitBranch: "master"}
	require.NoError(t, h.DB.CreateRelease(ctx, unpub))

	req := httptest.NewRequest("GET", "/api/projects/latestempty/releases/latest", nil)
	req.SetPathValue("project", "latestempty")
	req.SetPathValue("version", "latest")
	req = withProjectRoute(req, proj)
	rec := httptest.NewRecorder()
	h.GetRelease(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// Note: GetRelease auth (private project, project not found) is tested via
// requireProject middleware in the auth package.

func TestListReleases_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "listrel", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	require.NoError(t, h.DB.CreateRelease(ctx, &db.Release{ProjectID: proj.ID, Version: "1.0.0", VersionNum: 1000000}))
	require.NoError(t, h.DB.CreateRelease(ctx, &db.Release{ProjectID: proj.ID, Version: "2.0.0", VersionNum: 2000000}))

	req := httptest.NewRequest("GET", "/api/projects/listrel/releases", nil)
	req.SetPathValue("project", "listrel")
	req = withProjectRoute(req, proj)
	rec := httptest.NewRecorder()
	h.ListReleases(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var releases []db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &releases))
	assert.Equal(t, 2, len(releases))
}

// Note: ListReleases auth (private project, project not found) is tested via
// requireProject middleware in the auth package.

func TestCreateRelease_OciUser(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "ociuserproj", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"version":"1.0.0","oci_user":"65532:65532"}`
	req := httptest.NewRequest("POST", "/api/projects/ociuserproj/releases", strings.NewReader(body))
	req.SetPathValue("project", "ociuserproj")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var rel db.Release
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	assert.Equal(t, "65532:65532", rel.OciUser)

	// And it is persisted.
	got, err := h.DB.GetRelease(ctx, proj.ID, "1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "65532:65532", got.OciUser)
}

func TestCreateRelease_InvalidOciUser(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "badociuser", Versioning: db.VersioningSemver}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	body := `{"version":"1.0.0","oci_user":"root; rm -rf /"}`
	req := httptest.NewRequest("POST", "/api/projects/badociuser/releases", strings.NewReader(body))
	req.SetPathValue("project", "badociuser")
	req = withProjectRoute(req, proj)
	req = req.WithContext(writeToken(req.Context(), "read,write"))
	rec := httptest.NewRecorder()
	h.CreateRelease(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid oci_user")
}

func TestValidOCIUser(t *testing.T) {
	valid := []string{"root", "nonroot", "65532", "65532:65532", "nonroot:nonroot", "1000:1000", "app", "_svc", "a-b:c-d"}
	for _, s := range valid {
		assert.True(t, validOCIUser(s), "expected %q to be valid", s)
	}
	invalid := []string{"", ":", "65532:", ":65532", "root:", "-bad", "9bad", "root group", "root;rm", "u@h", "12345678901", strings.Repeat("a", 33)}
	for _, s := range invalid {
		assert.False(t, validOCIUser(s), "expected %q to be invalid", s)
	}
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
