package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// buildRouteInfo is a read route that names one build with a fixed override.
type buildRouteInfo struct {
	project string
	mode    db.Visibility
}

func (r buildRouteInfo) ProjectName() string { return r.project }
func (r buildRouteInfo) Access() AccessLevel { return ReadAccess }
func (r buildRouteInfo) BuildVisibility(context.Context, *db.DB, *db.Project) db.Visibility {
	return r.mode
}

func TestRequireProject_BuildVisibility(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	initTestMiddleware(t, d)
	ctx := context.Background()
	require.NoError(t, d.CreateProject(ctx, &db.Project{Name: "priv", IsPrivate: true, Versioning: "auto"}))
	require.NoError(t, d.CreateProject(ctx, &db.Project{Name: "pub", IsPrivate: false, Versioning: "auto"}))

	serve := func(project string, mode db.Visibility) (int, bool) {
		var sawPrivate bool
		parse := func(r *http.Request) RouteInfo { return buildRouteInfo{project: project, mode: mode} }
		handler := requireProjectFunc(parse, func(w http.ResponseWriter, r *http.Request) {
			sawPrivate = ReadIsPrivate(r.Context())
			w.WriteHeader(http.StatusOK)
		})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		return rec.Code, sawPrivate
	}

	code, priv := serve("priv", db.VisibilityPublic)
	assert.Equal(t, http.StatusOK, code, "a forced-public build of a private project serves without a token")
	assert.False(t, priv)

	code, _ = serve("priv", db.VisibilityAuto)
	assert.Equal(t, http.StatusUnauthorized, code, "an auto build follows its private project")

	code, _ = serve("pub", db.VisibilityPrivate)
	assert.Equal(t, http.StatusUnauthorized, code, "a forced-private build of a public project needs a token")

	code, priv = serve("pub", db.VisibilityAuto)
	assert.Equal(t, http.StatusOK, code)
	assert.False(t, priv)
}
