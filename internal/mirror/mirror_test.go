package mirror

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

const indexType = "application/vnd.oci.image.index.v1+json"

func digestOf(b string) string {
	sum := sha256.Sum256([]byte(b))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fakeRegistry answers like Docker Hub: every registry call needs a bearer
// token from its own token endpoint.
type fakeRegistry struct {
	*httptest.Server
	mu        sync.Mutex
	manifests map[string]string // reference -> body
	blobs     map[string]string // digest -> body
	calls     map[string]int    // "METHOD path" -> count
	down      bool
	// lie makes the blob endpoint send a body that does not match its digest.
	lie bool
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	f := &fakeRegistry{manifests: map[string]string{}, blobs: map[string]string{}, calls: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRegistry) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		json.NewEncoder(w).Encode(map[string]any{"token": "t-" + r.URL.Query().Get("scope"), "expires_in": 300})
		return
	}
	f.calls[r.Method+" "+r.URL.Path]++
	if f.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	repo, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/manifests/")
	kind := "manifests"
	if rest == "" {
		repo, rest, _ = strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/blobs/")
		kind = "blobs"
	}
	if r.Header.Get("Authorization") != "Bearer t-repository:"+repo+":pull" {
		w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="fake",scope="repository:%s:pull"`, f.URL, repo))
		http.Error(w, `{"errors":[{"code":"UNAUTHORIZED"}]}`, http.StatusUnauthorized)
		return
	}
	var body string
	var ok bool
	if kind == "manifests" {
		body, ok = f.manifests[rest]
		w.Header().Set("Content-Type", indexType)
	} else {
		body, ok = f.blobs[rest]
	}
	if !ok {
		http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Docker-Content-Digest", digestOf(body))
	if f.lie {
		body += "x"
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if r.Method == http.MethodGet {
		io.WriteString(w, body)
	}
}

func (f *fakeRegistry) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

type fixture struct {
	h   *Handler
	up  *fakeRegistry
	mux *http.ServeMux
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)
	up := newFakeRegistry(t)
	h := &Handler{Up: Upstream{Service: "dockerhub", Remote: up.URL}, DB: d, Store: store, TmpDir: t.TempDir(), Client: up.Client()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/{$}", h.Root)
	mux.HandleFunc("HEAD /v2/{$}", h.Root)
	mux.HandleFunc("GET /v2/{path...}", h.ServeHTTP)
	mux.HandleFunc("HEAD /v2/{path...}", h.ServeHTTP)
	return &fixture{h: h, up: up, mux: mux}
}

func (f *fixture) do(t *testing.T, method, path string, header http.Header, token bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	if token {
		req = req.WithContext(auth.WithToken(req.Context(), &db.APIToken{Scopes: "read"}))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestMirrorNeedsAReadToken(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodGet, "/v2/", nil, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "an open mirror spends the server's bandwidth for anyone")
	assert.Contains(t, rec.Header().Get("Www-Authenticate"), "Basic")
	rec = f.do(t, http.MethodGet, "/v2/library/alpine/manifests/latest", nil, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Zero(t, f.up.count("GET /v2/library/alpine/manifests/latest"), "an anonymous request must not reach the upstream")
	assert.Equal(t, http.StatusOK, f.do(t, http.MethodGet, "/v2/", nil, true).Code)
}

func TestMirrorRefusesWrites(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodPost, "/v2/library/alpine/blobs/uploads/", nil, true)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "a job must never change what a later job pulls")
}

func TestManifestByTagIsFetchedOnceAndServedFromTheStore(t *testing.T) {
	f := newFixture(t)
	index := `{"schemaVersion":2,"manifests":[]}`
	f.up.manifests["latest"] = index
	f.up.manifests[digestOf(index)] = index

	for range 2 {
		rec := f.do(t, http.MethodGet, "/v2/library/alpine/manifests/latest", nil, true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, index, rec.Body.String())
		assert.Equal(t, digestOf(index), rec.Header().Get("Docker-Content-Digest"))
		assert.Equal(t, indexType, rec.Header().Get("Content-Type"))
	}
	assert.Equal(t, 1, f.up.count("HEAD /v2/library/alpine/manifests/latest"), "a tag inside TagTTL must not ask the upstream again")
	assert.Equal(t, 1, f.up.count("GET /v2/library/alpine/manifests/"+digestOf(index)))

	rec := f.do(t, http.MethodGet, "/v2/library/alpine/manifests/"+digestOf(index), nil, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, f.up.count("GET /v2/library/alpine/manifests/"+digestOf(index)), "a stored manifest must come from the store")
}

func TestStaleTagServesWhenTheUpstreamIsDown(t *testing.T) {
	f := newFixture(t)
	index := `{"schemaVersion":2}`
	f.up.manifests["3"] = index
	f.up.manifests[digestOf(index)] = index
	require.Equal(t, http.StatusOK, f.do(t, http.MethodGet, "/v2/library/alpine/manifests/3", nil, true).Code)

	_, err := f.h.DB.Exec(`UPDATE mirror_tags SET checked_at = datetime('now', '-1 hour')`)
	require.NoError(t, err)
	f.up.down = true
	rec := f.do(t, http.MethodGet, "/v2/library/alpine/manifests/3", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, "an upstream outage must not stop a pull the mirror holds: %s", rec.Body.String())
	assert.Equal(t, index, rec.Body.String())
	assert.Equal(t, 2, f.up.count("HEAD /v2/library/alpine/manifests/3"), "a stale tag asks the upstream first")
}

func TestUnknownManifestIs404AndOutageIs502(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, http.MethodGet, "/v2/library/nothing/manifests/latest", nil, true)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "MANIFEST_UNKNOWN")

	f.up.down = true
	rec = f.do(t, http.MethodGet, "/v2/library/other/manifests/latest", nil, true)
	assert.Equal(t, http.StatusBadGateway, rec.Code, "an outage is never reported as an absent image")
	assert.Contains(t, rec.Body.String(), "503")
}

func TestManifestOfATypeTheClientRefusesIs404(t *testing.T) {
	f := newFixture(t)
	f.up.manifests["latest"] = `{}`
	rec := f.do(t, http.MethodGet, "/v2/library/alpine/manifests/latest", http.Header{"Accept": {"application/vnd.docker.distribution.manifest.v2+json"}}, true)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), indexType)
}

func TestBlobIsStreamedStoredAndThenServedWithRanges(t *testing.T) {
	f := newFixture(t)
	layer := strings.Repeat("layer-bytes ", 1000)
	d := digestOf(layer)
	f.up.blobs[d] = layer

	rec := f.do(t, http.MethodGet, "/v2/library/alpine/blobs/"+d, nil, true)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, layer, rec.Body.String())
	stored, found, err := f.h.DB.GetMirrorBlob(t.Context(), "dockerhub", d)
	require.NoError(t, err)
	require.True(t, found, "a fetched blob must land in the store")
	assert.Equal(t, int64(len(layer)), stored.Size)

	rec = f.do(t, http.MethodGet, "/v2/library/alpine/blobs/"+d, http.Header{"Range": {"bytes=6-10"}}, true)
	assert.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, layer[6:11], rec.Body.String())
	rec = f.do(t, http.MethodHead, "/v2/library/alpine/blobs/"+d, nil, true)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fmt.Sprint(len(layer)), rec.Header().Get("Content-Length"))
	assert.Equal(t, 1, f.up.count("GET /v2/library/alpine/blobs/"+d), "a stored blob must come from the store")
	assert.Zero(t, f.up.count("HEAD /v2/library/alpine/blobs/"+d))
}

func TestBlobThatDoesNotMatchItsDigestIsNotStored(t *testing.T) {
	f := newFixture(t)
	layer := "real"
	d := digestOf(layer)
	f.up.blobs[d] = layer
	f.up.lie = true
	f.do(t, http.MethodGet, "/v2/library/alpine/blobs/"+d, nil, true)
	_, found, err := f.h.DB.GetMirrorBlob(t.Context(), "dockerhub", d)
	require.NoError(t, err)
	assert.False(t, found, "content that does not match its digest must never be served to a later pull")
}

func TestJanitorEvictsBlobsNothingRead(t *testing.T) {
	f := newFixture(t)
	index := `{"schemaVersion":2}`
	f.up.manifests["latest"] = index
	f.up.manifests[digestOf(index)] = index
	require.Equal(t, http.StatusOK, f.do(t, http.MethodGet, "/v2/library/alpine/manifests/latest", nil, true).Code)
	stored, _, err := f.h.DB.GetMirrorBlob(t.Context(), "dockerhub", digestOf(index))
	require.NoError(t, err)
	_, err = f.h.DB.Exec(`UPDATE mirror_blobs SET used_at = datetime('now', '-8 days')`)
	require.NoError(t, err)

	j := &Janitor{DB: f.h.DB, Store: f.h.Store, Keep: 7 * 24 * time.Hour, Disk: func(string) (diskusage.Usage, error) { return diskusage.Usage{}, nil }}
	require.NoError(t, j.Sweep(t.Context(), time.Now()))
	assert.Equal(t, 1, LastReport().Expired)
	_, found, err := f.h.DB.GetMirrorBlob(t.Context(), "dockerhub", digestOf(index))
	require.NoError(t, err)
	assert.False(t, found)
	_, known, err := f.h.DB.GetMirrorTag(t.Context(), "dockerhub", "library/alpine", "latest")
	require.NoError(t, err)
	assert.False(t, known, "a tag must not outlive the manifest it names")
	exists, err := f.h.Store.Exists(t.Context(), stored.StorageKey)
	require.NoError(t, err)
	assert.False(t, exists, "eviction must free the bytes")
}

func TestParsePath(t *testing.T) {
	d := digestOf("x")
	for _, tc := range []struct {
		in   string
		want path
		ok   bool
	}{
		{"library/alpine/manifests/latest", path{"library/alpine", "manifests", "latest"}, true},
		{"actions/actions-runner/manifests/" + d, path{"actions/actions-runner", "manifests", d}, true},
		{"library/alpine/blobs/" + d, path{"library/alpine", "blobs", d}, true},
		{"library/alpine/blobs/latest", path{}, false},
		{"library/alpine/blobs/uploads/", path{}, false},
		{"../etc/manifests/latest", path{}, false},
		{"Library/alpine/manifests/latest", path{}, false},
		{"manifests/latest", path{}, false},
	} {
		got, ok := parsePath(tc.in)
		assert.Equal(t, tc.ok, ok, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
}

func TestParseChallenge(t *testing.T) {
	scheme, p := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/alpine:pull"`)
	assert.Equal(t, "Bearer", scheme)
	assert.Equal(t, map[string]string{"realm": "https://auth.docker.io/token", "service": "registry.docker.io", "scope": "repository:library/alpine:pull"}, p)
}
