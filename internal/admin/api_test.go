package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestAPIDashboard(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/dashboard", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	stats := resp["stats"].(map[string]any)
	assert.Equal(t, float64(1), stats["project_count"])
	assert.Equal(t, float64(1), stats["release_count"])
	assert.Equal(t, float64(1), stats["artifact_count"])
	assert.Equal(t, float64(2048), stats["total_storage_bytes"])

	build := resp["build"].(map[string]any)
	assert.Equal(t, "v1.2.3", build["version"])
	assert.Equal(t, "abc123def456", build["short_commit"])
	assert.Contains(t, build["commit_url"], "github.com/wow-look-at-my/buildhost/commit/abc123def456789000aabbccdd")

	recent := resp["recent"].([]any)
	assert.Len(t, recent, 1)
	assert.Equal(t, "testproject", recent[0].(map[string]any)["project_name"])

	assert.NotEmpty(t, resp["uptime"])
	assert.Contains(t, resp["cpu_percent"], "%")
}

func TestAPIDashboard_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/dashboard", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	recent := resp["recent"].([]any)
	assert.Empty(t, recent)
}

func TestAPIProjects(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/projects", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "testproject", resp[0]["name"])
	assert.Equal(t, float64(1), resp[0]["release_count"])
}

func TestAPIProjects_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/projects", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIProject(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/projects/testproject", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "testproject", project["name"])

	releases := resp["releases"].([]any)
	assert.Len(t, releases, 1)
	assert.Equal(t, "1.0.0", releases[0].(map[string]any)["version"])
}

func TestAPIProject_SlashNamespaced(t *testing.T) {
	srv, database := newTestServer(t)
	ctx := context.Background()
	p := &db.Project{Name: "cc-marketplace/recommend-go-toolchain", Versioning: db.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))

	w := serve(srv, http.MethodGet, "/api/projects/cc-marketplace/recommend-go-toolchain", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "cc-marketplace/recommend-go-toolchain", project["name"])
}

func TestAPIProject_NotFound(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/projects/nonexistent", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRelease(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/testproject/releases/1.0.0", nil)
	req.Host = "buildhost.example.com"
	w := httptest.NewRecorder()
	srv.NewHTTPServer().Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	project := resp["project"].(map[string]any)
	assert.Equal(t, "testproject", project["name"])

	release := resp["release"].(map[string]any)
	assert.Equal(t, "1.0.0", release["version"])

	artifacts := resp["artifacts"].([]any)
	assert.Len(t, artifacts, 1)
	a := artifacts[0].(map[string]any)
	assert.Equal(t, "linux", a["os"])
	assert.Equal(t, "amd64", a["arch"])
	assert.Equal(t, float64(2048), a["size"])

	assert.Equal(t, float64(2048), resp["total_size"])
	// The admin dashboard runs on its own subdomain (buildhost.example.com here);
	// base_url is the registry root and service URLs are real per-service hosts.
	assert.Equal(t, "https://example.com", resp["base_url"])
	assertServiceURLs(t, resp)
}

// assertServiceURLs checks the "services" map carries the real per-service
// subdomain hosts the router serves, derived from the request Host
// (buildhost.example.com -> example.com) with the admin label stripped.
func assertServiceURLs(t *testing.T, resp map[string]any) {
	t.Helper()
	services, ok := resp["services"].(map[string]any)
	require.True(t, ok, "response is missing a services map")
	for _, svc := range []string{"dl", "apt", "brew", "npm", "oci", "sites", "static"} {
		assert.Equal(t, "https://"+svc+".example.com", services[svc])
	}
}

func TestAPIRelease_SlashNamespaced(t *testing.T) {
	srv, database := newTestServer(t)
	ctx := context.Background()
	p := &db.Project{Name: "cc-marketplace/recommend-go-toolchain", Versioning: db.VersioningAuto}
	require.NoError(t, database.CreateProject(ctx, p))
	r := &db.Release{ProjectID: p.ID, Version: "v1", VersionNum: 1}
	require.NoError(t, database.CreateRelease(ctx, r))

	w := serve(srv, http.MethodGet, "/api/projects/cc-marketplace/recommend-go-toolchain/releases/v1", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	project := resp["project"].(map[string]any)
	assert.Equal(t, "cc-marketplace/recommend-go-toolchain", project["name"])
	release := resp["release"].(map[string]any)
	assert.Equal(t, "v1", release["version"])
}

func TestAPIRelease_NotFoundProject(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/projects/nope/releases/1.0.0", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRelease_NotFoundVersion(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/projects/testproject/releases/9.9.9", nil)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRegistries(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	req := httptest.NewRequest(http.MethodGet, "/api/registries", nil)
	req.Host = "buildhost.example.com"
	w := httptest.NewRecorder()
	srv.NewHTTPServer().Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "https://example.com", resp["base_url"])
	assertServiceURLs(t, resp)

	projects := resp["projects"].([]any)
	assert.Len(t, projects, 1)
	assert.Equal(t, "testproject", projects[0].(map[string]any)["name"])
}

func TestAPIOIDC(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/oidc", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Contains(t, resp[0]["issuer"], "token.actions.githubusercontent.com")
	assert.Equal(t, "testproject", resp[0]["project_name"])
}

func TestAPIOIDC_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/oidc", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPISites(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	ctx := context.Background()
	p, _ := database.GetProject(ctx, "testproject")
	_, err := database.UpsertSite(ctx, &db.Site{
		ProjectID: p.ID, Branch: "main", StorageKey: "sitekey1",
		Size: 4096, SHA256: "sitehash", FileCount: 10, GitCommit: "abc123",
	})
	require.NoError(t, err)

	w := serve(srv, http.MethodGet, "/api/sites", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	sites := resp["sites"].([]any)
	assert.Len(t, sites, 1)
	assert.Equal(t, "testproject", sites[0].(map[string]any)["project_name"])
	assert.Equal(t, "main", sites[0].(map[string]any)["branch"])
}

func TestAPISites_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/sites", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	sites := resp["sites"].([]any)
	assert.Empty(t, sites)
}

func TestAPIProject_IncludesSites(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	ctx := context.Background()
	p, _ := database.GetProject(ctx, "testproject")
	_, err := database.UpsertSite(ctx, &db.Site{
		ProjectID: p.ID, Branch: "dev", StorageKey: "sitekey2",
		Size: 2048, SHA256: "sitehash2", FileCount: 5,
	})
	require.NoError(t, err)

	w := serve(srv, http.MethodGet, "/api/projects/testproject", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	sites := resp["sites"].([]any)
	assert.Len(t, sites, 1)
	assert.Equal(t, "dev", sites[0].(map[string]any)["branch"])
}

func TestAPIArtifacts(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/artifacts", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "testproject", resp[0]["project_name"])
	assert.Equal(t, "1.0.0", resp[0]["version"])
	assert.Equal(t, "linux", resp[0]["os"])
	assert.Equal(t, "amd64", resp[0]["arch"])
}

func TestAPIArtifacts_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/artifacts", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestAPIStorage(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	w := serve(srv, http.MethodGet, "/api/storage", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	projects := resp["projects"].([]any)
	assert.Len(t, projects, 1)
	assert.Equal(t, "testproject", projects[0].(map[string]any)["name"])
	assert.Equal(t, float64(2048), projects[0].(map[string]any)["total_bytes"])
	assert.Equal(t, float64(2048), resp["total_bytes"])

	// Component breakdown fields are exposed for the reconciliation view.
	assert.Equal(t, float64(2048), resp["logical_bytes"])
	assert.Equal(t, float64(0), resp["stripped_bytes"])
	assert.Equal(t, float64(0), resp["debug_bytes"])
	assert.Equal(t, float64(0), resp["packaged_bytes"])
}

func TestAPIStorage_Empty(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/storage", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	projects := resp["projects"].([]any)
	assert.Empty(t, projects)
}

func TestAPISidebar(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/sidebar", nil)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	build := resp["build"].(map[string]any)
	assert.Equal(t, "v1.2.3", build["version"])
	assert.Equal(t, "abc123def456", build["short_commit"])
	assert.Contains(t, resp["cpu_percent"], "%")
}
