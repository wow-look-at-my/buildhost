package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequireProject_WriteUnauthorized_ExplainsOIDCReason proves that when a
// JWT was presented and rejected, the 401 carries the rejection reason rather
// than a bare "authentication required" -- so a CI caller can see what to fix.
func TestRequireProject_WriteUnauthorized_ExplainsOIDCReason(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)
	require.NoError(t, d.CreateProject(context.Background(), &db.Project{Name: "foo", Versioning: "auto"}))

	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "foo", access: WriteAccess}
	}
	handler := requireProjectFunc(parse, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run on unauthorized write")
	})

	decodeError := func(rec *httptest.ResponseRecorder) string {
		var resp struct {
			Error string `json:"error"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp.Error
	}

	// With a recorded OIDC failure, the reason is surfaced.
	ctx := WithOIDCError(context.Background(), errors.New(`org "evil" not in allowed list`))
	req := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	msg := decodeError(rec)
	assert.Contains(t, msg, "authentication required")
	assert.Contains(t, msg, `org "evil" not in allowed list`)

	// Without one, the message stays bare (no token at all).
	req2 := httptest.NewRequest("POST", "/", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
	bare := decodeError(rec2)
	assert.Contains(t, bare, "authentication required")
	assert.NotContains(t, bare, "OIDC token rejected")
}
