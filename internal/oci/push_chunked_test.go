package oci

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// startUploadSession opens a chunked upload session and returns its uuid.
func startUploadSession(t *testing.T, h *Handler, proj *db.Project) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/v2/"+proj.Name+"/blobs/uploads/", nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	uuid := rec.Header().Get("Docker-Upload-UUID")
	require.NotEmpty(t, uuid)
	return uuid
}

func patchChunk(t *testing.T, h *Handler, proj *db.Project, uuid string, body []byte, contentRange string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PATCH", "/v2/"+proj.Name+"/blobs/uploads/"+uuid, bytes.NewReader(body))
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPush_ChunkedWithContentRange(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	uuid := startUploadSession(t, h, proj)
	first, second := []byte("chunk-one-"), []byte("chunk-two")

	rec := patchChunk(t, h, proj, uuid, first, fmt.Sprintf("0-%d", len(first)-1))
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, fmt.Sprintf("0-%d", len(first)-1), rec.Header().Get("Range"))

	rec = patchChunk(t, h, proj, uuid, second, fmt.Sprintf("%d-%d", len(first), len(first)+len(second)-1))
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, fmt.Sprintf("0-%d", len(first)+len(second)-1), rec.Header().Get("Range"))

	content := append(append([]byte{}, first...), second...)
	digest := digestOf(content)
	req := httptest.NewRequest("PUT", "/v2/"+proj.Name+"/blobs/uploads/"+uuid+"?digest="+digest, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, digest, rec.Header().Get("Docker-Content-Digest"))
}

func TestPush_ChunkOffsetMismatchIs416AndResumable(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	uuid := startUploadSession(t, h, proj)
	first := []byte("first-chunk")
	rec := patchChunk(t, h, proj, uuid, first, fmt.Sprintf("0-%d", len(first)-1))
	require.Equal(t, http.StatusAccepted, rec.Code)

	// Re-sending the same chunk (a retry after a lost response) must be
	rec = patchChunk(t, h, proj, uuid, first, fmt.Sprintf("0-%d", len(first)-1))
	require.Equal(t, http.StatusRequestedRangeNotSatisfiable, rec.Code)
	assert.Equal(t, fmt.Sprintf("0-%d", len(first)-1), rec.Header().Get("Range"))
	assert.Equal(t, uuid, rec.Header().Get("Docker-Upload-UUID"))

	second := []byte("-second")
	rec = patchChunk(t, h, proj, uuid, second, fmt.Sprintf("%d-%d", len(first), len(first)+len(second)-1))
	require.Equal(t, http.StatusAccepted, rec.Code)

	content := append(append([]byte{}, first...), second...)
	req := httptest.NewRequest("PUT", "/v2/"+proj.Name+"/blobs/uploads/"+uuid+"?digest="+digestOf(content), nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestPush_MalformedContentRange(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	uuid := startUploadSession(t, h, proj)
	rec := patchChunk(t, h, proj, uuid, []byte("data"), "bytes 0-3/10")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPush_UploadStatus(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := &db.Project{Name: "ollama", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(t.Context(), proj))

	uuid := startUploadSession(t, h, proj)

	req := httptest.NewRequest("GET", "/v2/"+proj.Name+"/blobs/uploads/"+uuid, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "0-0", rec.Header().Get("Range"))

	// After a chunk the committed range is reported.
	require.Equal(t, http.StatusAccepted, patchChunk(t, h, proj, uuid, []byte("0123456789"), "").Code)
	req = httptest.NewRequest("GET", "/v2/"+proj.Name+"/blobs/uploads/"+uuid, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: uuid})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "0-9", rec.Header().Get("Range"))
	assert.Equal(t, uuid, rec.Header().Get("Docker-Upload-UUID"))

	req = httptest.NewRequest("GET", "/v2/"+proj.Name+"/blobs/uploads/nope", nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "uploads", reference: "nope"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUploadStore_SweepGoesByActivity(t *testing.T) {
	h, _, _ := setupTest(t)

	sess, err := h.uploads.start()
	require.NoError(t, err)

	// Old session, recent activity: an in-flight chunked upload must survive.
	sess.mu.Lock()
	sess.created = time.Now().Add(-3 * time.Hour)
	sess.lastActive = time.Now()
	sess.mu.Unlock()
	h.uploads.sweep(2 * time.Hour)
	assert.NotNil(t, h.uploads.get(sess.uuid), "active session must not be swept")

	// Idle past the window: swept.
	sess.mu.Lock()
	sess.lastActive = time.Now().Add(-3 * time.Hour)
	sess.mu.Unlock()
	h.uploads.sweep(2 * time.Hour)
	assert.Nil(t, h.uploads.get(sess.uuid), "idle session must be swept")
}

func TestRoute_UploadsAlwaysWrite(t *testing.T) {
	// The GET status read is push-flow state: it must never be reachable with
	rt := route{project: "p", action: "uploads", reference: "u", method: http.MethodGet}
	assert.Equal(t, auth.WriteAccess, rt.Access())
	// Pull routes stay read.
	assert.Equal(t, auth.ReadAccess, route{project: "p", action: "blobs", reference: "sha256:x", method: http.MethodGet}.Access())
}
