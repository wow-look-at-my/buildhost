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

// mockServer implements the session protocol plus one capture-everything
// upload target, so tests can drive the client against realistic behavior
// (offset checks, partial chunks, transient failures) without a real server.
type mockServer struct {
	t  *testing.T
	mu sync.Mutex

	maxDirect int64 // advertised by /api/v1/server-info; 0 omits the endpoint

	sessions map[string][]byte // id -> spooled bytes
	nextID   int

	// failAppends fails this many appends with a 500 AFTER committing the
	// chunk -- the lost-response case a resuming client must survive.
	failAppends int

	// brokenAppends fails appends with a 500 WITHOUT committing anything --
	// a server that cannot make progress at all.
	brokenAppends bool

	// disableSessions makes POST /api/v1/uploads 404 (an old server).
	disableSessions bool

	sessionCalls int // POST /api/v1/uploads count

	// captured final upload (direct body or finalized session)
	captured      []byte
	capturedQuery map[string]string
	capturedCT    string
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
		json.NewEncoder(w).Encode(map[string]any{"max_direct_upload_bytes": m.maxDirect, "upload_sessions": true})

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
	m, ts := newMockServer(t)
	path, data := tempFile(t, 8) // under the advertised 10-byte limit

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
	m, ts := newMockServer(t)
	path, data := tempFile(t, 100) // over the advertised 10-byte limit

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 7} // 15 chunks
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
	m, ts := newMockServer(t)
	path, data := tempFile(t, 50)
	m.failAppends = 2 // two chunk responses vanish after the bytes landed

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 8}
	resp, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	assert.Equal(t, data, m.captured, "client recovered the committed size and resumed")
}

func TestChunkSizeDisabledForcesDirect(t *testing.T) {
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
	m, ts := newMockServer(t)
	m.maxDirect = 0 // no server-info endpoint at all
	path, data := tempFile(t, 100)

	// Default threshold is 90 MiB, so this 100-byte file goes direct.
	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 7}
	resp, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, data, m.captured)
	assert.Equal(t, 0, m.sessionCalls)
}

func TestOldServerFallsBackToDirect(t *testing.T) {
	m, ts := newMockServer(t)
	m.disableSessions = true // POST /api/v1/uploads 404s (old buildhost)
	path, data := tempFile(t, 100)

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 7}
	resp, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, data, m.captured, "falls back to the classic single request")
	assert.Equal(t, 1, m.sessionCalls, "tried the session endpoint once")
}

func TestNoProgressGivesUpAndAborts(t *testing.T) {
	m, ts := newMockServer(t)
	path, _ := tempFile(t, 50)
	m.brokenAppends = true // every append 500s without committing anything

	u := &Uploader{Server: ts.URL, Token: "tok", ChunkSize: 8}
	_, err := u.Upload("PUT", ts.URL+"/target", nil, path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no progress")
	assert.Empty(t, m.sessions, "session aborted after the hard failure")
}
