package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func seedDownloadLinkProject(t *testing.T, h *Handler) *db.Project {
	t.Helper()
	ctx := context.Background()
	proj := &db.Project{Name: "dlproj", Versioning: db.VersioningAuto}
	require.NoError(t, h.DB.CreateProject(ctx, proj))
	rel := &db.Release{ProjectID: proj.ID, Version: "1", VersionNum: 1}
	require.NoError(t, h.DB.CreateRelease(ctx, rel))
	art := &db.Artifact{ReleaseID: rel.ID, OS: db.OSLinux, Arch: db.ArchAMD64, Kind: db.KindBinary, StorageKey: "k", Size: 10, SHA256: "h"}
	require.NoError(t, h.DB.CreateArtifact(ctx, art))
	return proj
}

func shareTokenCtx(ctx context.Context, scopes string, projectID *int64) context.Context {
	return auth.WithToken(ctx, &db.APIToken{ID: 1, Scopes: scopes, ProjectID: projectID})
}

func postDownloadLink(h *Handler, project, body string, ctxMod func(context.Context) context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/projects/"+project+"/download-links", strings.NewReader(body))
	req.SetPathValue("project", project)
	if ctxMod != nil {
		req = req.WithContext(ctxMod(req.Context()))
	}
	rec := httptest.NewRecorder()
	h.CreateDownloadLink(rec, req)
	return rec
}

func TestCreateDownloadLink_Success(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)

	rec := postDownloadLink(h, "dlproj", `{"os":"linux","arch":"amd64","version":"1"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", nil)
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Contains(t, resp.URL, "/file?")
	assert.Contains(t, resp.URL, "project=dlproj")
	assert.Contains(t, resp.URL, "token=bhdl_")
	assert.True(t, strings.HasPrefix(resp.Token, "bhdl_"))
	// The minted token authorizes exactly this artifact.
	assert.True(t, auth.VerifyDownloadToken(resp.Token, "dlproj", "1", "linux", "amd64", "raw", false))
	assert.False(t, auth.VerifyDownloadToken(resp.Token, "dlproj", "1", "linux", "arm64", "raw", false))
}

func TestCreateDownloadLink_RequiresShareScope(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)
	rec := postDownloadLink(h, "dlproj", `{"os":"linux","arch":"amd64","version":"1"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "read,write", nil)
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateDownloadLink_NoToken(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)
	rec := postDownloadLink(h, "dlproj", `{"os":"linux","arch":"amd64","version":"1"}`, nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateDownloadLink_ProjectNotFound(t *testing.T) {
	h := setupTestHandler(t)
	rec := postDownloadLink(h, "ghost", `{"os":"linux","arch":"amd64","version":"1"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", nil)
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreateDownloadLink_TokenNotAuthorizedForProject(t *testing.T) {
	h := setupTestHandler(t)
	proj := seedDownloadLinkProject(t, h)
	other := proj.ID + 999
	rec := postDownloadLink(h, "dlproj", `{"os":"linux","arch":"amd64","version":"1"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", &other)
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCreateDownloadLink_ArtifactNotFound(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)
	rec := postDownloadLink(h, "dlproj", `{"os":"darwin","arch":"arm64","version":"1"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", nil)
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreateDownloadLink_RejectsAny(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)
	rec := postDownloadLink(h, "dlproj", `{"os":"any","arch":"any","version":"1"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", nil)
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateDownloadLink_MissingFields(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)
	rec := postDownloadLink(h, "dlproj", `{"os":"linux"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", nil)
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateDownloadLink_UnsupportedFmt(t *testing.T) {
	h := setupTestHandler(t)
	seedDownloadLinkProject(t, h)
	rec := postDownloadLink(h, "dlproj", `{"os":"linux","arch":"amd64","version":"1","fmt":"bogus"}`, func(c context.Context) context.Context {
		return shareTokenCtx(c, "share", nil)
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClampDownloadLinkTTL(t *testing.T) {
	assert.Equal(t, defaultDownloadLinkTTL, clampDownloadLinkTTL(0))
	assert.Equal(t, minDownloadLinkTTL, clampDownloadLinkTTL(1))        // 1s clamped up to min
	assert.Equal(t, maxDownloadLinkTTL, clampDownloadLinkTTL(99999999)) // huge clamped down to max
	assert.Equal(t, 2*time.Hour, clampDownloadLinkTTL(7200))
}
