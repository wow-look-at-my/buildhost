package uploadclient

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	RetryBaseDelay = time.Millisecond
}

type mockServer struct {
	t  *testing.T
	mu sync.Mutex

	maxDirect int64

	// uploadBySHA256 is advertised on server-info when set (a server with
	uploadBySHA256 bool

	sessions map[string][]byte // id -> spooled bytes
	nextID   int

	failAppends int

	brokenAppends bool

	disableSessions bool

	sessionCalls int // POST /api/v1/uploads count

	// captured final upload (direct body or finalized session)
	captured      []byte
	capturedQuery map[string]string
	capturedCT    string
	capturedHdr   http.Header
}

func newMockServer(t *testing.T) (*mockServer, *httptest.Server) {
	m := &mockServer{t: t, sessions: map[string][]byte{}, maxDirect: 10}
	ts := httptest.NewServer(m)
	t.Cleanup(ts.Close)
	return m, ts
}

func (m *mockServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case r.URL.Path == "/api/v1/server-info":
		if m.maxDirect == 0 {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"max_direct_upload_bytes": m.maxDirect,
			"upload_sessions":         true,
			"upload_by_sha256":        m.uploadBySHA256,
		})

	case r.URL.Path == "/api/v1/uploads" && r.Method == "POST":
		m.sessionCalls++
		if m.disableSessions {
			http.NotFound(w, r)
			return
		}
		m.nextID++
		id := fmt.Sprintf("sess-%d", m.nextID)
		m.sessions[id] = []byte{}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": id, "size": 0})

	case len(r.URL.Path) > len("/api/v1/uploads/") && r.URL.Path[:len("/api/v1/uploads/")] == "/api/v1/uploads/":
		id := r.URL.Path[len("/api/v1/uploads/"):]
		buf, ok := m.sessions[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]any{"id": id, "size": len(buf)})
		case "DELETE":
			delete(m.sessions, id)
			w.WriteHeader(http.StatusNoContent)
		case "PATCH":
			if m.brokenAppends {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			if offset != len(buf) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]any{"error": "offset mismatch", "size": len(buf)})
				return
			}
			chunk, err := io.ReadAll(r.Body)
			require.NoError(m.t, err)
			m.sessions[id] = append(buf, chunk...)
			if m.failAppends > 0 {
				// Chunk committed, response lost: client must resume via GET.
				m.failAppends--
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "size": len(m.sessions[id])})
		}

	default:
		// The "real" upload endpoint: a direct body or a finalized session.
		q := r.URL.Query()
		m.capturedQuery = map[string]string{}
		for k := range q {
			m.capturedQuery[k] = q.Get(k)
		}
		m.capturedCT = r.Header.Get("Content-Type")
		m.capturedHdr = r.Header.Clone()
		if id := q.Get("upload_session"); id != "" {
			buf, ok := m.sessions[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			body, _ := io.ReadAll(r.Body)
			require.Empty(m.t, body, "finalize must carry an empty body")
			m.captured = buf
			delete(m.sessions, id)
		} else {
			body, err := io.ReadAll(r.Body)
			require.NoError(m.t, err)
			m.captured = body
		}
		w.WriteHeader(http.StatusCreated)
	}
}

func tempFile(t *testing.T, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "artifact.bin")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path, data
}

func TestSmallFileUploadsDirect(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	path, data := tempFile(t, 8)

	u := &Uploader{Server: ts.URL, Token: "tok"}
	resp, err := u.Upload("PUT", ts.URL+"/target?kind=binary", nil, path)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	assert.Equal(t, data, m.captured)
	assert.Empty(t, m.capturedQuery["upload_session"], "small file must not use a session")
	assert.Equal(t, 0, m.sessionCalls, "no session endpoints touched")
	assert.Equal(t, "binary", m.capturedQuery["kind"], "target query preserved")
}

func TestLargeFileChunks(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	path, data := tempFile(t, 100)

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 7}
	resp, err := u.Upload("PUT", ts.URL+"/target?kind=binary", map[string]string{"X-Extra": "yes"}, path)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	assert.Equal(t, data, m.captured, "assembled bytes identical to the file")
	assert.NotEmpty(t, m.capturedQuery["upload_session"])
	sum := sha256.Sum256(data)
	assert.Equal(t, hex.EncodeToString(sum[:]), m.capturedQuery["upload_sha256"])
	assert.Equal(t, "binary", m.capturedQuery["kind"], "original query params preserved on finalize")
	assert.Empty(t, m.sessions, "session consumed")
}

func TestChunkedResumesAfterLostResponse(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	path, data := tempFile(t, 50)
	m.failAppends = 2

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 8}
	resp, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	assert.Equal(t, data, m.captured, "client recovered the committed size and resumed")
}

func TestChunkSizeDisabledForcesDirect(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	path, data := tempFile(t, 100) // way over the advertised limit

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: -1}
	resp, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, data, m.captured)
	assert.Equal(t, 0, m.sessionCalls, "chunking disabled: single direct request")
}

func TestServerInfoUnavailableFallsBackToDefaultThreshold(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	m.maxDirect = 0 // no server-info endpoint at all
	path, data := tempFile(t, 100)

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 7}
	resp, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, data, m.captured)
	assert.Equal(t, 0, m.sessionCalls)
}

// A missing session endpoint is a broken server, not a mode to accommodate:
func TestMissingSessionEndpointFailsLoudly(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	m.disableSessions = true
	path, _ := tempFile(t, 100)

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 7}
	_, err := u.Upload("PUT", ts.URL+"/target", nil, path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create upload session")
	assert.Empty(t, m.captured, "nothing may be sent to the artifact endpoint instead")
	assert.Equal(t, 1, m.sessionCalls, "tried the session endpoint once")
}

func TestNoProgressGivesUpAndAborts(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	path, _ := tempFile(t, 50)
	m.brokenAppends = true // every append 500s without committing anything

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 8}
	_, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no progress")
	assert.Empty(t, m.sessions, "session aborted after the hard failure")
}

func TestFileSHA256(t *testing.T) {
	t.Serial()
	path, data := tempFile(t, 33)
	sum := sha256.Sum256(data)

	got, err := FileSHA256(path)
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(sum[:]), got)

	_, err = FileSHA256(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}

// The hash-reference capability must ONLY come from an explicit server-info
// advertisement: a server that predates the feature ignores upload_sha256 and
// would store the empty request body, so guessing is never safe.
func TestSupportsUploadBySHA256(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	m.uploadBySHA256 = true
	u := &Uploader{Server: ts.URL, Token: "tok"}
	assert.True(t, u.SupportsUploadBySHA256())

	// Advertised absent (an older server): reported false.
	_, ts2 := newMockServer(t)
	u2 := &Uploader{Server: ts2.URL, Token: "tok"}
	assert.False(t, u2.SupportsUploadBySHA256())

	// server-info unreachable: reported false, never guessed.
	m3, ts3 := newMockServer(t)
	m3.maxDirect = 0 // 404s server-info
	u3 := &Uploader{Server: ts3.URL, Token: "tok"}
	assert.False(t, u3.SupportsUploadBySHA256())
}

func TestUploadByHash(t *testing.T) {
	t.Serial()
	m, ts := newMockServer(t)
	u := &Uploader{Server: ts.URL, Token: "tok"}

	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	resp, err := u.UploadByHash("PUT", ts.URL+"/target?kind=binary",
		map[string]string{"X-Artifact-Filename": "tool.exe"}, sum)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	assert.Empty(t, m.captured, "a hash-reference upload carries no body")
	assert.Equal(t, sum, m.capturedQuery["upload_sha256"])
	assert.Equal(t, "binary", m.capturedQuery["kind"], "target query preserved")
	assert.Empty(t, m.capturedQuery["upload_session"], "never a session finalize")
	assert.Equal(t, "tool.exe", m.capturedHdr.Get("X-Artifact-Filename"), "per-slot headers ride the reference")
	assert.Empty(t, m.capturedCT, "no Content-Type on an empty-body reference")
}
