package server_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// siteTarGz builds a minimal site archive for deploy tests.
func siteTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Size: int64(len(content)), Mode: 0o644, Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// uploadSession mirrors the session endpoints' JSON responses.
type uploadSessionJSON struct {
	ID        string `json:"id"`
	Size      int64  `json:"size"`
	ExpiresAt string `json:"expires_at"`
	Error     string `json:"error"`
	MaxSize   int64  `json:"max_size"`
}

func (e *testEnv) createUploadSession(t *testing.T) uploadSessionJSON {
	t.Helper()
	resp := e.doRequest(t, "POST", "/api/v1/uploads", "", nil, true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var sess uploadSessionJSON
	decodeJSON(t, resp, &sess)
	require.NotEmpty(t, sess.ID)
	require.NotEmpty(t, sess.ExpiresAt)
	return sess
}

func (e *testEnv) appendChunk(t *testing.T, id string, offset int, chunk []byte) *http.Response {
	t.Helper()
	return e.doRequest(t, "PATCH", fmt.Sprintf("/api/v1/uploads/%s?offset=%d", id, offset),
		"application/octet-stream", bytes.NewReader(chunk), true)
}

// TestChunkedUploadMatchesDirectUpload proves the headline behavior: a
// multi-chunk upload finalized against the REAL artifact endpoint produces an
// artifact byte-identical to a direct PUT of the same payload, and the bytes
// round-trip through the download path.
func TestChunkedUploadMatchesDirectUpload(t *testing.T) {
	t.Serial()
	env := setup(t)

	payload := make([]byte, 300)
	_, err := rand.Read(payload)
	require.NoError(t, err)
	wantSHA := sha256.Sum256(payload)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"chunky","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	for _, body := range []string{`{"git_branch":"master"}`, `{"git_branch":"master"}`} {
		resp = env.postJSON(t, "/api/v1/projects/chunky/releases", body)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
	}

	sess := env.createUploadSession(t)
	offset := 0
	for _, n := range []int{100, 150, 50} {
		resp = env.appendChunk(t, sess.ID, offset, payload[offset:offset+n])
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var got uploadSessionJSON
		decodeJSON(t, resp, &got)
		offset += n
		require.Equal(t, int64(offset), got.Size)
	}

	// Finalize by reference on the real artifact endpoint, with integrity.
	finalizeURL := fmt.Sprintf("/api/v1/projects/chunky/releases/1/artifacts/linux/amd64?upload_session=%s&upload_sha256=%s",
		sess.ID, hex.EncodeToString(wantSHA[:]))
	resp = env.doRequest(t, "PUT", finalizeURL, "application/octet-stream", nil, true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var chunked db.Artifact
	decodeJSON(t, resp, &chunked)

	// Direct control upload of the same bytes.
	resp = env.putBody(t, "/api/v1/projects/chunky/releases/2/artifacts/linux/amd64", payload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var direct db.Artifact
	decodeJSON(t, resp, &direct)

	assert.Equal(t, hex.EncodeToString(wantSHA[:]), chunked.SHA256)
	assert.Equal(t, direct.SHA256, chunked.SHA256)
	assert.Equal(t, direct.Size, chunked.Size)
	assert.Equal(t, direct.StorageKey, chunked.StorageKey)

	// The session was consumed by the successful finalize.
	resp = env.doRequest(t, "GET", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// And the artifact actually serves: publish, then download the bytes.
	resp = env.postJSON(t, "/api/v1/projects/chunky/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = env.doSubdomainRequest(t, "GET", "static", "/file?arch=amd64&os=linux&project=chunky&v=1", "", nil, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, payload, readBody(t, resp))
}

// TestChunkedUploadMultiPlatformFanOut proves a chunked upload session
func TestChunkedUploadMultiPlatformFanOut(t *testing.T) {
	t.Serial()
	env := setup(t)

	payload := make([]byte, 200)
	_, err := rand.Read(payload)
	require.NoError(t, err)
	wantSHA := sha256.Sum256(payload)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"fanout","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/fanout/releases", `{"git_branch":"master"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	sess := env.createUploadSession(t)
	offset := 0
	for _, n := range []int{120, 80} {
		resp = env.appendChunk(t, sess.ID, offset, payload[offset:offset+n])
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
		offset += n
	}

	finalizeURL := fmt.Sprintf("/api/v1/projects/fanout/releases/1/artifacts/linux,darwin,windows/amd64?upload_session=%s&upload_sha256=%s",
		sess.ID, hex.EncodeToString(wantSHA[:]))
	resp = env.doRequest(t, "PUT", finalizeURL, "application/octet-stream", nil, true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var artifacts []db.Artifact
	decodeJSON(t, resp, &artifacts)
	require.Len(t, artifacts, 3)
	for _, a := range artifacts {
		assert.Equal(t, hex.EncodeToString(wantSHA[:]), a.SHA256)
		assert.Equal(t, artifacts[0].StorageKey, a.StorageKey, "all rows must share one blob")
	}
	assert.Equal(t, db.OSLinux, artifacts[0].OS)
	assert.Equal(t, db.OSDarwin, artifacts[1].OS)
	assert.Equal(t, db.OSWindows, artifacts[2].OS)

	resp = env.doRequest(t, "GET", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Every fanned-out platform serves the same bytes through the normal
	resp = env.postJSON(t, "/api/v1/projects/fanout/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	for _, osName := range []string{"linux", "darwin", "windows"} {
		resp = env.doSubdomainRequest(t, "GET", "static", "/file?arch=amd64&os="+osName+"&project=fanout&v=1", "", nil, true)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, payload, readBody(t, resp))
	}
}

func TestUploadSessionResume(t *testing.T) {
	t.Serial()
	env := setup(t)

	sess := env.createUploadSession(t)

	resp := env.appendChunk(t, sess.ID, 0, []byte("hello "))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = env.appendChunk(t, sess.ID, 3, []byte("world"))
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	var conflict uploadSessionJSON
	decodeJSON(t, resp, &conflict)
	require.Equal(t, int64(6), conflict.Size)

	// A status read reports the same size; resuming from it succeeds.
	resp = env.doRequest(t, "GET", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var status uploadSessionJSON
	decodeJSON(t, resp, &status)
	require.Equal(t, int64(6), status.Size)

	resp = env.appendChunk(t, sess.ID, int(status.Size), []byte("world"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var done uploadSessionJSON
	decodeJSON(t, resp, &done)
	require.Equal(t, int64(11), done.Size)
}

func TestUploadSessionOwnerIsolation(t *testing.T) {
	t.Serial()
	env := setup(t)

	otherToken, _, err := env.database.CreateToken(context.Background(), "other", nil, "read,write")
	require.NoError(t, err)

	sess := env.createUploadSession(t)
	asOther := func(method, path string, body io.Reader) *http.Response {
		req, err := http.NewRequest(method, env.ts.URL+path, body)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+otherToken)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	for _, probe := range []struct{ method, path string }{
		{"GET", "/api/v1/uploads/" + sess.ID},
		{"PATCH", "/api/v1/uploads/" + sess.ID + "?offset=0"},
		{"DELETE", "/api/v1/uploads/" + sess.ID},
	} {
		resp := asOther(probe.method, probe.path, bytes.NewReader([]byte("x")))
		require.Equal(t, http.StatusNotFound, resp.StatusCode, "%s %s", probe.method, probe.path)
		resp.Body.Close()
	}

	// Cross-identity finalize is refused the same way.
	resp := asOther("PUT", "/api/v1/projects/nope/releases/1/artifacts/linux/amd64?upload_session="+sess.ID, nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// The owner still sees it.
	resp = env.doRequest(t, "GET", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestUploadSessionSHA256Mismatch(t *testing.T) {
	t.Serial()
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"shaproj","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/shaproj/releases", `{}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	sess := env.createUploadSession(t)
	resp = env.appendChunk(t, sess.ID, 0, []byte("actual bytes"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	finalize := func(sha string) string {
		return fmt.Sprintf("/api/v1/projects/shaproj/releases/1/artifacts/linux/amd64?upload_session=%s&upload_sha256=%s",
			sess.ID, sha)
	}
	resp = env.doRequest(t, "PUT", finalize(hexSHA256([]byte("what the client thinks it sent"))),
		"application/octet-stream", nil, true)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := string(readBody(t, resp))
	assert.Contains(t, body, "sha256 mismatch")

	// No artifact was created and the session survives, so the client can
	resp = env.doRequest(t, "GET", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = env.doRequest(t, "PUT", finalize(hexSHA256([]byte("actual bytes"))), "application/octet-stream", nil, true)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "finalize with the right hash succeeds after a mismatch")
	resp.Body.Close()
}

func hexSHA256(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestUploadSessionAbortAndAuth(t *testing.T) {
	t.Serial()
	env := setup(t)

	// Session creation requires a credential with write scope.
	resp := env.doRequest(t, "POST", "/api/v1/uploads", "", nil, false)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	readToken, _, err := env.database.CreateToken(context.Background(), "ro", nil, "read")
	require.NoError(t, err)
	req, err := http.NewRequest("POST", env.ts.URL+"/api/v1/uploads", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+readToken)
	roResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, roResp.StatusCode)
	roResp.Body.Close()

	// Abort removes the session.
	sess := env.createUploadSession(t)
	resp = env.doRequest(t, "DELETE", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()
	resp = env.doRequest(t, "GET", "/api/v1/uploads/"+sess.ID, "", nil, true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// A finalize with a non-empty body is rejected: the session IS the body.
	sess = env.createUploadSession(t)
	resp = env.doRequest(t, "PUT", "/api/v1/projects/p/releases/1/artifacts/linux/amd64?upload_session="+sess.ID,
		"application/octet-stream", bytes.NewReader([]byte("both?")), true)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	resp = env.doRequest(t, "PUT", "/api/v1/projects/p/releases/1/artifacts/linux/amd64?upload_session=00000000000000000000000000000000",
		"application/octet-stream", nil, true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Bad offset parameter.
	sess2 := env.createUploadSession(t)
	resp = env.doRequest(t, "PATCH", "/api/v1/uploads/"+sess2.ID, "application/octet-stream",
		bytes.NewReader([]byte("x")), true)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// TestUploadSessionTooLarge proves the total-size cap applies at append time,
// not just at finalize.
func TestUploadSessionTooLarge(t *testing.T) {
	t.Serial()
	t.Setenv("BUILDHOST_MAX_UPLOAD_SIZE", "16")
	env := setup(t)

	sess := env.createUploadSession(t)
	resp := env.appendChunk(t, sess.ID, 0, []byte("0123456789"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = env.appendChunk(t, sess.ID, 10, []byte("0123456789"))
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	var over uploadSessionJSON
	decodeJSON(t, resp, &over)
	assert.Equal(t, int64(10), over.Size, "over-cap chunk rolled back")
	assert.Equal(t, int64(16), over.MaxSize)
}

// TestServerInfo covers the discovery endpoint clients use to choose direct
// vs chunked BEFORE sending anything.
func TestServerInfo(t *testing.T) {
	t.Serial()
	env := setup(t)

	resp := env.get(t, "/api/v1/server-info")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info struct {
		MaxDirectUploadBytes int64 `json:"max_direct_upload_bytes"`
		MaxUploadBytes       int64 `json:"max_upload_bytes"`
		UploadSessions       bool  `json:"upload_sessions"`
	}
	decodeJSON(t, resp, &info)
	assert.Equal(t, int64(95<<20), info.MaxDirectUploadBytes, "default stays under Cloudflare's 100 MB edge cap")
	assert.Equal(t, int64(2<<30), info.MaxUploadBytes)
	assert.True(t, info.UploadSessions)
}

func TestChunkedSiteDeploy(t *testing.T) {
	t.Serial()
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"sitey","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	archive := siteTarGz(t, map[string]string{"index.html": "<h1>chunked</h1>"})

	sess := env.createUploadSession(t)
	half := len(archive) / 2
	for _, part := range []struct {
		off  int
		data []byte
	}{{0, archive[:half]}, {half, archive[half:]}} {
		resp = env.appendChunk(t, sess.ID, part.off, part.data)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// Finalize on the sites subdomain endpoint; Content-Type still selects the
	resp = env.doSubdomainRequest(t, "PUT", "sites", "/sitey/branch/main?upload_session="+sess.ID,
		"application/gzip", nil, true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.doSubdomainRequest(t, "GET", "sites", "/sitey/index.html", "", nil, true)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "<h1>chunked</h1>", string(readBody(t, resp)))
}
