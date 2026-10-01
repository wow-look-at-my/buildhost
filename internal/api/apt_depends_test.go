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

func TestCreateRelease_AptDependsDeclaration(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "deptool", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	createRelease := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		got, err := h.DB.GetProject(ctx, "deptool")
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "/api/projects/deptool/releases", strings.NewReader(body))
		req.SetPathValue("project", "deptool")
		req = withProjectRoute(req, got)
		req = req.WithContext(writeToken(req.Context(), "read,write"))
		rec := httptest.NewRecorder()
		h.CreateRelease(rec, req)
		return rec
	}
	depends := func() string {
		t.Helper()
		got, err := h.DB.GetProject(ctx, "deptool")
		require.NoError(t, err)
		return got.AptDepends
	}

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main","apt_depends":"bubblewrap | docker.io"}`).Code)
	assert.Equal(t, "bubblewrap | docker.io", depends())

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main"}`).Code)
	assert.Equal(t, "bubblewrap | docker.io", depends(), "an absent field leaves the stored value")

	rec := createRelease(`{"git_branch":"main","apt_depends":"bubblewrap\nDescription: owned"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "apt_depends")
	assert.Equal(t, "bubblewrap | docker.io", depends(), "a refused value changes nothing")
	_, err := h.DB.GetRelease(ctx, proj.ID, "3")
	require.ErrorIs(t, err, db.ErrNotFound, "a refused publish creates no release")

	require.Equal(t, http.StatusCreated, createRelease(`{"git_branch":"main","apt_depends":""}`).Code)
	assert.Empty(t, depends(), "an empty value clears it")
}

func TestUpdateProjectSettings_AptDepends(t *testing.T) {
	t.Serial()
	h := setupTestHandler(t)
	ctx := context.Background()

	proj := &db.Project{Name: "depproj", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))

	patch := func(body string) *httptest.ResponseRecorder {
		got, err := h.DB.GetProject(ctx, "depproj")
		require.NoError(t, err)
		req := httptest.NewRequest("PATCH", "/api/v1/projects/depproj", strings.NewReader(body))
		req.SetPathValue("project", "depproj")
		req = withProjectRoute(req, got)
		rec := httptest.NewRecorder()
		h.UpdateProjectSettings(rec, req)
		return rec
	}

	rec := patch(`{"apt_depends":"bubblewrap | docker.io"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var p db.Project
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "bubblewrap | docker.io", p.AptDepends)

	rec = patch(`{"apt_depends":"x\nPre-Depends: evil","create_service":true}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	got, err := h.DB.GetProject(ctx, "depproj")
	require.NoError(t, err)
	assert.Equal(t, "bubblewrap | docker.io", got.AptDepends)
	assert.False(t, got.CreateService, "a refused request changes nothing")
}
