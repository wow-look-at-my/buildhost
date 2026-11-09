package mirror

import (
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
	"github.com/wow-look-at-my/router"
)

// fakeGitHub answers a release download the way github.com does: a redirect
// to a CDN host, which sends the bytes.
type fakeGitHub struct {
	*httptest.Server
	mu     sync.Mutex
	assets map[string]string // "/owner/repo/releases/download/tag/asset" -> body
	calls  map[string]int
	status int
	// short makes the CDN stop before the Content-Length it sent.
	short bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	f := &fakeGitHub{assets: map[string]string{}, calls: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name, ok := strings.CutPrefix(r.URL.EscapedPath(), "/cdn"); ok {
		body := f.assets[name]
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if f.short {
			body = body[:len(body)/2]
		}
		io.WriteString(w, body)
		return
	}
	f.calls[r.Method+" "+r.URL.EscapedPath()]++
	if f.status != 0 {
		http.Error(w, "upstream says no", f.status)
		return
	}
	if _, ok := f.assets[r.URL.EscapedPath()]; !ok {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/cdn"+r.URL.EscapedPath(), http.StatusFound)
}

func (f *fakeGitHub) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls["GET "+path]
}

type releaseFixture struct {
	h  *ReleaseHandler
	up *fakeGitHub
	rt *router.Router
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { d.Close() })
	store, err := storage.NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)
	up := newFakeGitHub(t)
	h := &ReleaseHandler{Handler: Handler{Up: Upstream{Service: ReleaseUpstream.Service, Remote: up.URL}, DB: d, Store: store, TmpDir: t.TempDir(), Client: up.Client()}}
	// The mirror owns its subdomain, so its pattern is the only route.
	rt := router.New()
	rt.HandleFunc(h.Pattern(), router.Allow, h.ServeHTTP)
	return &releaseFixture{h: h, up: up, rt: rt}
}

func (f *releaseFixture) do(t *testing.T, method, path string, header http.Header, token bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/"+ReleaseRoot+path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	if token {
		req = req.WithContext(auth.WithToken(req.Context(), &db.APIToken{Scopes: "read"}))
	}
	rec := httptest.NewRecorder()
	f.rt.ServeHTTP(rec, req)
	return rec
}

const protocAsset = "/protocolbuffers/protobuf/releases/download/v29.3/protoc-29.3-linux-x86_64.zip"

func TestReleaseMirrorNeedsAReadToken(t *testing.T) {
	f := newReleaseFixture(t)
	f.up.assets[protocAsset] = "zip"
	rec := f.do(t, http.MethodGet, protocAsset, nil, false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "an open mirror spends the server's bandwidth for anyone")
	assert.Contains(t, rec.Header().Get("Www-Authenticate"), "Basic")
	assert.Zero(t, f.up.count(protocAsset), "an anonymous request must not reach GitHub")
}

func TestReleaseMirrorRefusesWrites(t *testing.T) {
	f := newReleaseFixture(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := f.do(t, method, protocAsset, nil, true)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, method)
	}
}

func TestReleaseResponsesStayOutOfSharedCaches(t *testing.T) {
	f := newReleaseFixture(t)
	f.up.assets[protocAsset] = "zip"
	for range 2 {
		rec := f.do(t, http.MethodGet, protocAsset, nil, true)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "private", rec.Header().Get("Cache-Control"), "the CDN caches a .zip by extension, and a shared copy skips the token check")
		assert.Equal(t, "Authorization", rec.Header().Get("Vary"))
	}
}

func TestReleaseAssetIsFetchedOnceAndServedFromTheStore(t *testing.T) {
	f := newReleaseFixture(t)
	body := strings.Repeat("protoc-bytes ", 1000)
	f.up.assets[protocAsset] = body

	for range 2 {
		rec := f.do(t, http.MethodGet, protocAsset, nil, true)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, body, rec.Body.String())
		assert.Equal(t, `attachment; filename=protoc-29.3-linux-x86_64.zip`, rec.Header().Get("Content-Disposition"))
	}
	assert.Equal(t, 1, f.up.count(protocAsset), "a stored asset must come from the store")

	digest, known, err := f.h.DB.GetMirrorReleaseAsset(t.Context(), ReleaseUpstream.Service, "protocolbuffers/protobuf", "v29.3", "protoc-29.3-linux-x86_64.zip")
	require.NoError(t, err)
	require.True(t, known)
	assert.Equal(t, digestOf(body), digest)

	rec := f.do(t, http.MethodGet, protocAsset, http.Header{"Range": {"bytes=7-11"}}, true)
	assert.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, body[7:12], rec.Body.String())
	rec = f.do(t, http.MethodHead, protocAsset, nil, true)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fmt.Sprint(len(body)), rec.Header().Get("Content-Length"))
	assert.Equal(t, 1, f.up.count(protocAsset))
}

func TestReleaseRepositoryCaseDoesNotFetchAgain(t *testing.T) {
	f := newReleaseFixture(t)
	f.up.assets[protocAsset] = "zip"
	require.Equal(t, http.StatusOK, f.do(t, http.MethodGet, protocAsset, nil, true).Code)
	rec := f.do(t, http.MethodGet, "/ProtocolBuffers/Protobuf/releases/download/v29.3/protoc-29.3-linux-x86_64.zip", nil, true)
	require.Equal(t, http.StatusOK, rec.Code, "GitHub ignores the case of owner and repo, so the mirror must too")
	assert.Equal(t, "zip", rec.Body.String())
}

func TestReleaseTagWithASlash(t *testing.T) {
	f := newReleaseFixture(t)
	asset := "/owner/repo/releases/download/cli/v1.2/tool.tar.gz"
	f.up.assets[asset] = "tgz"
	rec := f.do(t, http.MethodGet, asset, nil, true)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, known, err := f.h.DB.GetMirrorReleaseAsset(t.Context(), ReleaseUpstream.Service, "owner/repo", "cli/v1.2", "tool.tar.gz")
	require.NoError(t, err)
	assert.True(t, known)
}

func TestUnknownReleaseAssetIs404AndOutageIs502(t *testing.T) {
	f := newReleaseFixture(t)
	rec := f.do(t, http.MethodGet, "/owner/repo/releases/download/v1/missing.zip", nil, true)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	for status, want := range map[int]int{http.StatusGatewayTimeout: http.StatusBadGateway, http.StatusTooManyRequests: http.StatusTooManyRequests} {
		f.up.status = status
		rec = f.do(t, http.MethodGet, protocAsset, nil, true)
		assert.Equal(t, want, rec.Code, "GitHub answered %d", status)
		assert.Contains(t, rec.Body.String(), fmt.Sprint(status))
	}
}

func TestReleaseAssetThatBreaksOffIsNotStored(t *testing.T) {
	f := newReleaseFixture(t)
	f.up.assets[protocAsset] = strings.Repeat("x", 4096)
	f.up.short = true
	f.do(t, http.MethodGet, protocAsset, nil, true)
	_, known, err := f.h.DB.GetMirrorReleaseAsset(t.Context(), ReleaseUpstream.Service, "protocolbuffers/protobuf", "v29.3", "protoc-29.3-linux-x86_64.zip")
	require.NoError(t, err)
	assert.False(t, known, "a partial asset must never be served to a later download")
}

func TestJanitorEvictsReleaseAssets(t *testing.T) {
	f := newReleaseFixture(t)
	f.up.assets[protocAsset] = "zip"
	require.Equal(t, http.StatusOK, f.do(t, http.MethodGet, protocAsset, nil, true).Code)
	_, err := f.h.DB.Exec(`UPDATE mirror_blobs SET used_at = datetime('now', '-8 days')`)
	require.NoError(t, err)

	j := &Janitor{DB: f.h.DB, Store: f.h.Store, Keep: 7 * 24 * time.Hour, Disk: func(string) (diskusage.Usage, error) { return diskusage.Usage{}, nil }}
	require.NoError(t, j.Sweep(t.Context(), time.Now()))
	_, known, err := f.h.DB.GetMirrorReleaseAsset(t.Context(), ReleaseUpstream.Service, "protocolbuffers/protobuf", "v29.3", "protoc-29.3-linux-x86_64.zip")
	require.NoError(t, err)
	assert.False(t, known, "an asset row must not outlive the blob it names")

	require.Equal(t, http.StatusOK, f.do(t, http.MethodGet, protocAsset, nil, true).Code)
	assert.Equal(t, 2, f.up.count(protocAsset), "an evicted asset comes from GitHub again")
}

func TestOnlyReleaseAssetPathsReachGitHub(t *testing.T) {
	f := newReleaseFixture(t)
	for _, p := range []string{
		"/o/r/releases/download/v1",
		"/o/r/releases/latest/download/a.zip",
		"/o/r/archive/refs/tags/v1.tar.gz",
		"/o/r/raw/main/README.md",
		"/o/r",
		"/o/../releases/download/v1/a.zip",
		"/o/r/releases/download/../a.zip",
		"/o/r/releases/download/v1/..",
		"/-o/r/releases/download/v1/a.zip",
		"/o/r/releases/download/v%201/a.zip",
		"/a/b/c/releases/download/v1/a.zip",
	} {
		rec := f.do(t, http.MethodGet, p, nil, true)
		assert.Equal(t, http.StatusNotFound, rec.Code, p)
	}
	f.up.mu.Lock()
	defer f.up.mu.Unlock()
	assert.Empty(t, f.up.calls, "a path that is not a release asset must never reach GitHub")
}

func TestReleasePathOf(t *testing.T) {
	for _, tc := range []struct {
		owner, repo, tag, asset string
		want                    releasePath
		ok                      bool
	}{
		{"BurntSushi", "ripgrep", "15.0.0", "ripgrep-15.0.0-aarch64-apple-darwin.tar.gz", releasePath{"BurntSushi/ripgrep", "15.0.0", "ripgrep-15.0.0-aarch64-apple-darwin.tar.gz"}, true},
		{"joseluisq", "macosx-sdks", "14.5", "MacOSX14.5.sdk.tar.xz", releasePath{"joseluisq/macosx-sdks", "14.5", "MacOSX14.5.sdk.tar.xz"}, true},
		{"o", "r", "cli/v1", "a.zip", releasePath{"o/r", "cli/v1", "a.zip"}, true},
		{"o/x", "r", "v1", "a.zip", releasePath{}, false},
		{"o", "..", "v1", "a.zip", releasePath{}, false},
		{"o", "r", "..", "a.zip", releasePath{}, false},
		{"o", "r", "v1", "..", releasePath{}, false},
		{"o", "r", "v1", "a/b.zip", releasePath{}, false},
		{"o", "r", "", "a.zip", releasePath{}, false},
	} {
		got, ok := releasePathOf(tc.owner, tc.repo, tc.tag, tc.asset)
		assert.Equal(t, tc.ok, ok, "%+v", tc)
		assert.Equal(t, tc.want, got, "%+v", tc)
	}
}
