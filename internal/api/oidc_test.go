package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func globalWriteCtx() context.Context {
	tok := &db.APIToken{ID: 99999, Scopes: "read,write"}
	return auth.WithToken(context.Background(), tok)
}

func TestCreateOIDCPolicy_Success(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"https://token.actions.githubusercontent.com","subject_pattern":"repo:myorg/myrepo:*","scopes":"read,write"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var p db.OIDCPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "https://token.actions.githubusercontent.com", p.Issuer)
	assert.Equal(t, "repo:myorg/myrepo:*", p.SubjectPattern)
}

func TestCreateOIDCPolicy_MissingFields(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"","subject_pattern":""}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOIDCPolicy_InvalidScope(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"https://example.com","subject_pattern":"*","scopes":"admin"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateOIDCPolicy_RequiresGlobalToken(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"https://example.com","subject_pattern":"*"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	projID := int64(1)
	tok := &db.APIToken{ID: 1, Scopes: "read,write", ProjectID: &projID}
	req = req.WithContext(auth.WithToken(context.Background(), tok))
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateOIDCPolicy_NoAuth(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"https://example.com","subject_pattern":"*"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateOIDCPolicy_Duplicate(t *testing.T) {
	h := setupTestHandler(t)
	ctx := globalWriteCtx()

	body := `{"issuer":"https://example.com","subject_pattern":"sub:test"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)

	req = httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(ctx)
	rec = httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestListOIDCPolicies_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	require.NoError(t, h.DB.CreateOIDCPolicy(ctx, &db.OIDCPolicy{
		Issuer:	"https://example.com", SubjectPattern: "sub:1", Scopes: "read",
	}))

	req := httptest.NewRequest("GET", "/api/v1/oidc/policies", nil)
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.ListOIDCPolicies(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var policies []db.OIDCPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &policies))
	assert.GreaterOrEqual(t, len(policies), 1)
}

func TestListOIDCPolicies_RequiresGlobalToken(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/oidc/policies", nil)
	projID := int64(1)
	tok := &db.APIToken{ID: 1, Scopes: "read,write", ProjectID: &projID}
	req = req.WithContext(auth.WithToken(context.Background(), tok))
	rec := httptest.NewRecorder()
	h.ListOIDCPolicies(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDeleteOIDCPolicy_Success(t *testing.T) {
	h := setupTestHandler(t)
	ctx := context.Background()

	p := &db.OIDCPolicy{Issuer: "https://example.com", SubjectPattern: "sub:del", Scopes: "read"}
	require.NoError(t, h.DB.CreateOIDCPolicy(ctx, p))

	req := httptest.NewRequest("DELETE", "/api/v1/oidc/policies/"+strconv.FormatInt(p.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.DeleteOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDeleteOIDCPolicy_NotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("DELETE", "/api/v1/oidc/policies/9999", nil)
	req.SetPathValue("id", "9999")
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.DeleteOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreateOIDCPolicy_WithAudience(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"https://example.com","subject_pattern":"sub:*","audience":"https://buildhost.example.com","scopes":"read"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var p db.OIDCPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Equal(t, "https://buildhost.example.com", p.Audience)
}

func TestCreateOIDCPolicy_ScopesNormalizedWithSpaces(t *testing.T) {
	h := setupTestHandler(t)

	body := `{"issuer":"https://example.com","subject_pattern":"sub:spaced","scopes":"read, write"}`
	req := httptest.NewRequest("POST", "/api/v1/oidc/policies", strings.NewReader(body))
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.CreateOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestDeleteOIDCPolicy_InvalidID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("DELETE", "/api/v1/oidc/policies/abc", nil)
	req.SetPathValue("id", "abc")
	req = req.WithContext(globalWriteCtx())
	rec := httptest.NewRecorder()
	h.DeleteOIDCPolicy(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
