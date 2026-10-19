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

// dependsFixture creates api-cli plus the projects it can depend on, and a release-create call scoped by tokenFor.
func dependsFixture(t *testing.T, tokenFor func(context.Context, *db.Project) context.Context) (*Handler, func(string) *httptest.ResponseRecorder, func() string) {
	t.Helper()
	h := setupTestHandler(t)
	ctx := context.Background()
	for _, p := range []*db.Project{
		{Name: "api-cli", Versioning: db.VersioningAuto},
		{Name: "ascii-timeline", Versioning: db.VersioningAuto},
		{Name: "secret-lib", Versioning: db.VersioningAuto, IsPrivate: true},
	} {
		require.NoError(t, h.DB.CreateProject(ctx, p))
	}
	createRelease := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		got, err := h.DB.GetProject(ctx, "api-cli")
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/projects/api-cli/releases", strings.NewReader(body))
		req.SetPathValue("project", "api-cli")
		req = withProjectRoute(req, got)
		req = req.WithContext(tokenFor(req.Context(), got))
		rec := httptest.NewRecorder()
		h.CreateRelease(rec, req)
		return rec
	}
	depends := func() string {
		t.Helper()
		got, err := h.DB.GetProject(ctx, "api-cli")
		require.NoError(t, err)
		return got.Depends
	}
	return h, createRelease, depends
}

func anyProjectToken(ctx context.Context, _ *db.Project) context.Context {
	return writeToken(ctx, "read,write")
}

func TestCreateRelease_DependsDeclaration(t *testing.T) {
	t.Serial()
	_, createRelease, depends := dependsFixture(t, anyProjectToken)

	rec := createRelease(`{"git_branch":"main","depends":["ascii-timeline","ascii-timeline","secret-lib"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "ascii-timeline,secret-lib", depends(), "stored sorted and deduplicated")

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main"}`).Code)
	assert.Equal(t, "ascii-timeline,secret-lib", depends(), "an absent field leaves the stored value")

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main","depends":[]}`).Code)
	assert.Empty(t, depends(), "an empty list clears it")
}

func TestCreateRelease_UnknownDependencyFailsLoud(t *testing.T) {
	t.Serial()
	h, createRelease, depends := dependsFixture(t, anyProjectToken)

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main","depends":["ascii-timeline"]}`).Code)
	rec := createRelease(`{"git_branch":"main","depends":["ascii-timeline","no-such-project"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `depends: unknown project \"no-such-project\"`)
	assert.Equal(t, "ascii-timeline", depends(), "a refused value changes nothing")
	proj, err := h.DB.GetProject(context.Background(), "api-cli")
	require.NoError(t, err)
	_, err = h.DB.GetRelease(context.Background(), proj.ID, "2")
	require.ErrorIs(t, err, db.ErrNotFound, "a refused publish creates no release")
}

func TestCreateRelease_UnreadableDependencyIsUnknown(t *testing.T) {
	t.Serial()
	_, createRelease, depends := dependsFixture(t, func(ctx context.Context, p *db.Project) context.Context {
		return projectWriteToken(ctx, p.ID)
	})

	rec := createRelease(`{"git_branch":"main","depends":["secret-lib"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `depends: unknown project \"secret-lib\"`,
		"the refusal is the same as for a missing project, so it confirms nothing about a private one")
	assert.Empty(t, depends())

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main","depends":["ascii-timeline"]}`).Code,
		"a public dependency needs no read grant")
}

func TestCreateRelease_DependsRefusesSelfAndCycles(t *testing.T) {
	t.Serial()
	h, createRelease, depends := dependsFixture(t, anyProjectToken)
	dep, err := h.DB.GetProject(context.Background(), "ascii-timeline")
	require.NoError(t, err)
	require.NoError(t, h.DB.SetProjectDepends(context.Background(), dep.ID, []string{"api-cli"}))

	rec := createRelease(`{"git_branch":"main","depends":["api-cli"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "cannot depend on itself")

	rec = createRelease(`{"git_branch":"main","depends":["ascii-timeline"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "cycle")

	rec = createRelease(`{"git_branch":"main","depends":["Not A Name"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "is not a project name")
	assert.Empty(t, depends())
}

func TestCreateRelease_DependsResolvesAlias(t *testing.T) {
	t.Serial()
	h, createRelease, depends := dependsFixture(t, anyProjectToken)
	dep, err := h.DB.GetProject(context.Background(), "ascii-timeline")
	require.NoError(t, err)
	require.NoError(t, h.DB.RenameProject(context.Background(), dep.ID, "ascii-timeline", "ascii-tl"))

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main","depends":["ascii-timeline"]}`).Code)
	assert.Equal(t, "ascii-tl", depends(), "an old name stores the project's current name")
}

func TestUpdateProjectSettings_Depends(t *testing.T) {
	t.Serial()
	h, _, depends := dependsFixture(t, anyProjectToken)
	ctx := context.Background()

	patch := func(body string) *httptest.ResponseRecorder {
		got, err := h.DB.GetProject(ctx, "api-cli")
		require.NoError(t, err)
		req := httptest.NewRequest("PATCH", "/api/v1/projects/api-cli", strings.NewReader(body))
		req.SetPathValue("project", "api-cli")
		req = withProjectRoute(req, got)
		req = req.WithContext(writeToken(req.Context(), "read,write"))
		rec := httptest.NewRecorder()
		h.UpdateProjectSettings(rec, req)
		return rec
	}

	rec := patch(`{"depends":["ascii-timeline"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var p db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "ascii-timeline", p.Depends)

	rec = patch(`{"depends":["missing"],"create_service":true}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, "ascii-timeline", depends())
	got, err := h.DB.GetProject(ctx, "api-cli")
	require.NoError(t, err)
	assert.False(t, got.CreateService, "a refused request changes nothing")
}
