package brew

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smartTapServer serves the handler's smart+dumb tap endpoints under the real
// git-subdomain paths so an actual git client can talk to them.
func smartTapServer(t *testing.T, h *Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /brew/tap.git/info/refs", h.ServeTapInfoRefs)
	mux.HandleFunc("POST /brew/tap.git/git-upload-pack", h.ServeTapUploadPack)
	mux.HandleFunc("GET /brew/tap.git/", h.ServeTap)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// lineageDirFor returns the single on-disk lineage directory (tests create
func lineageDirFor(t *testing.T, h *Handler) string {
	t.Helper()
	entries, err := os.ReadDir(h.tapHistoryRoot())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	return filepath.Join(h.tapHistoryRoot(), entries[0].Name())
}

// The smart advertisement must describe the SAME tip as the dumb path -- both
// read the lineage's persisted refs/heads/main -- and the info/refs route
// without a service parameter must keep serving the dumb file byte-for-byte.
func TestServeTapInfoRefs_AdvertisementDerivesFromLineageTip(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "go-toolchain", "binary")

	dumb := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, dumb.Code)
	tip := strings.Fields(dumb.Body.String())[0]

	req := httptest.NewRequest("GET", "/brew/tap.git/info/refs?service=git-upload-pack", nil)
	req.Host = "git.example.com"
	rec := httptest.NewRecorder()
	h.ServeTapInfoRefs(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-git-upload-pack-advertisement", rec.Header().Get("Content-Type"))
	adv := rec.Body.String()
	assert.Contains(t, adv, "# service=git-upload-pack")
	assert.Contains(t, adv, tip+" HEAD\x00")
	assert.Contains(t, adv, "symref=HEAD:refs/heads/main")
	assert.Contains(t, adv, tip+" refs/heads/main\n")

	// No service parameter: the exact dumb file serving, unchanged.
	req = httptest.NewRequest("GET", "/brew/tap.git/info/refs", nil)
	req.Host = "git.example.com"
	rec = httptest.NewRecorder()
	h.ServeTapInfoRefs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, dumb.Body.String(), rec.Body.String())

	// The tap is read-only: any other service is refused.
	req = httptest.NewRequest("GET", "/brew/tap.git/info/refs?service=git-receive-pack", nil)
	req.Host = "git.example.com"
	rec = httptest.NewRecorder()
	h.ServeTapInfoRefs(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// A plain (non-shallow) smart clone -- what `brew tap` runs -- must transfer
// the tap's FULL history: every commit reachable from the tip, so later dumb
// or smart fetches fast-forward. The client echoing advertised capabilities
// must not be mistaken for a shallow request (the old "expected ACK/NAK, got
// 'shallow <sha>'" failure).
func TestServeUploadPack_FullCloneTransfersWholeHistory(t *testing.T) {
	t.Serial()
	requireGit(t)

	oldTTL := tapCacheTTL
	tapCacheTTL = 0 // every request re-checks content, so publishes append immediately
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")
	ts := smartTapServer(t, h)

	require.Equal(t, http.StatusOK, mustGet(t, ts.URL+"/brew/tap.git/info/refs"))
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")

	dir := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", ts.URL+"/brew/tap.git", dir)

	_, err := os.Stat(filepath.Join(dir, ".git", "shallow"))
	require.True(t, os.IsNotExist(err), "a plain clone must not be shallow")
	assert.Equal(t, "2", strings.TrimSpace(runGit(t, dir, "rev-list", "--count", "origin/main")))
	runGit(t, dir, "fsck")

	for _, f := range []string{"appone.rb", "apptwo.rb"} {
		data, err := os.ReadFile(filepath.Join(dir, "Formula", f))
		require.NoError(t, err)
		assert.Contains(t, string(data), "< Formula")
	}
}

func TestServeUploadPack_ShallowCloneAndUnshallow(t *testing.T) {
	t.Serial()
	requireGit(t)

	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")
	ts := smartTapServer(t, h)

	require.Equal(t, http.StatusOK, mustGet(t, ts.URL+"/brew/tap.git/info/refs"))
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")

	dir := filepath.Join(t.TempDir(), "tap")
	runGit(t, t.TempDir(), "clone", "--depth", "1", ts.URL+"/brew/tap.git", dir)

	_, err := os.Stat(filepath.Join(dir, ".git", "shallow"))
	require.NoError(t, err, "a depth-1 clone of a 2-commit history must be shallow")
	assert.Equal(t, "1", strings.TrimSpace(runGit(t, dir, "rev-list", "--count", "origin/main")))
	data, err := os.ReadFile(filepath.Join(dir, "Formula", "apptwo.rb"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "< Formula")

	runGit(t, dir, "fetch", "--unshallow", "origin")
	_, err = os.Stat(filepath.Join(dir, ".git", "shallow"))
	require.True(t, os.IsNotExist(err), "--unshallow must clear the shallow marker")
	assert.Equal(t, "2", strings.TrimSpace(runGit(t, dir, "rev-list", "--count", "origin/main")))
}

func mustGet(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// The consistency guarantee for a publish landing mid-clone: the pack is
// built from the commit the client WANTED (the sha the advertisement handed
// it), which the append-only lineage still holds even though the tip has
// advanced past it.
func TestServeUploadPack_ServesRequestedWantAfterTipAdvance(t *testing.T) {
	t.Serial()
	requireGit(t)

	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	// The advertisement a client would have seen before the publish.
	rec := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec.Code)
	tip1 := strings.Fields(rec.Body.String())[0]

	// A publish lands before the client's fetch POST arrives.
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")

	var body bytes.Buffer
	body.Write(pktLineString("want " + tip1 + " multi_ack_detailed agent=test\n"))
	body.WriteString("0000")
	body.Write(pktLineString("done\n"))
	req := httptest.NewRequest("POST", "/brew/tap.git/git-upload-pack", &body)
	req.Host = "git.example.com"
	prec := httptest.NewRecorder()
	h.ServeTapUploadPack(prec, req)

	require.Equal(t, http.StatusOK, prec.Code)
	resp := prec.Body.Bytes()
	require.True(t, bytes.HasPrefix(resp, []byte("0008NAK\n")), "response must open with NAK: %q", resp[:min(len(resp), 16)])
	pack := resp[len("0008NAK\n"):]
	require.True(t, bytes.HasPrefix(pack, []byte("PACK")))

	// The tip really advanced underneath the fetch.
	newTip := readTapTip(lineageDirFor(t, h))
	require.NotEqual(t, tip1, newTip)

	// Real git accepts the pack and it carries the WANTED commit's closure.
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet", ".")
	unpack := exec.Command("git", "-C", dir, "unpack-objects", "-q")
	unpack.Stdin = bytes.NewReader(pack)
	out, err := unpack.CombinedOutput()
	require.NoErrorf(t, err, "git unpack-objects: %s", out)
	assert.Equal(t, "commit", strings.TrimSpace(runGit(t, dir, "cat-file", "-t", tip1)))
	assert.Contains(t, runGit(t, dir, "ls-tree", "-r", tip1), "Formula/appone.rb")
}

// A flush-terminated batch of haves (no "done" yet) is a negotiation round:
// the stateless server answers NAK -- and ONLY NAK, never a pack, which would
func TestServeUploadPack_NegotiationRoundAnswersNAKOnly(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	rec := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec.Code)
	tip := strings.Fields(rec.Body.String())[0]

	var body bytes.Buffer
	body.Write(pktLineString("want " + tip + " multi_ack_detailed\n"))
	body.WriteString("0000")
	body.Write(pktLineString("have " + strings.Repeat("1", 40) + "\n"))
	body.WriteString("0000")
	req := httptest.NewRequest("POST", "/brew/tap.git/git-upload-pack", &body)
	req.Host = "git.example.com"
	prec := httptest.NewRecorder()
	h.ServeTapUploadPack(prec, req)

	require.Equal(t, http.StatusOK, prec.Code)
	assert.Equal(t, "0008NAK\n", prec.Body.String())
}

func TestWantsShallow(t *testing.T) {
	t.Serial()
	// A full clone's want line echoes the advertised shallow capabilities;
	want := pktLineString("want 9f9969e4d487c9a700c13b5a2119a09de9262f31 multi_ack_detailed side-band-64k thin-pack ofs-delta deepen-since deepen-not agent=git/2.43.0\n")

	var full bytes.Buffer
	full.Write(want)
	full.WriteString("0000")
	full.Write(pktLineString("done\n"))
	assert.False(t, wantsShallow(full.Bytes()), "capability echo must not be mistaken for a deepen request")

	var shallow bytes.Buffer
	shallow.Write(want)
	shallow.Write(pktLineString("deepen 1\n"))
	shallow.WriteString("0000")
	assert.True(t, wantsShallow(shallow.Bytes()), "a deepen pkt-line is a depth request")
}

// The smart endpoints carry each tap root's exact credential semantics: the
// private root challenges anonymous requests, brew.{domain}/tap.git serves
func TestSmartTap_CredentialGates(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	req := httptest.NewRequest("GET", "/private/tap.git/info/refs?service=git-upload-pack", nil)
	req.Host = "brew.example.com"
	rec := httptest.NewRecorder()
	h.ServePrivateTapInfoRefs(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Www-Authenticate"), "Basic")

	// Anonymous /tap.git smart handshake on the brew host: served in place,
	req = httptest.NewRequest("GET", "/tap.git/info/refs?service=git-upload-pack", nil)
	req.Host = "brew.example.com:18080"
	rec = httptest.NewRecorder()
	h.ServeTapInfoRefs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-git-upload-pack-advertisement", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	assert.Contains(t, rec.Body.String(), "refs/heads/main")

	// ... and without a service parameter, the dumb info/refs file, in place.
	req = httptest.NewRequest("GET", "/tap.git/info/refs", nil)
	req.Host = "brew.example.com:18080"
	rec = httptest.NewRecorder()
	h.ServeTapInfoRefs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), "refs/heads/main")

	// The anonymous smart fetch is served in place on the brew host too.
	var anonBody bytes.Buffer
	anonBody.Write(pktLineString("want " + strings.Repeat("0", 40) + "\n"))
	anonBody.WriteString("0000")
	anonBody.Write(pktLineString("done\n"))
	req = httptest.NewRequest("POST", "/tap.git/git-upload-pack", &anonBody)
	req.Host = "brew.example.com:18080"
	rec = httptest.NewRecorder()
	h.ServeTapUploadPack(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte("0008NAK\nPACK")), "NAK + raw pack expected: %q", rec.Body.Bytes()[:min(rec.Body.Len(), 16)])

	// A credentialed advertisement is served in place, scoped by the lineage
	req = withReadToken(httptest.NewRequest("GET", "/private/tap.git/info/refs?service=git-upload-pack", nil), nil)
	req.Host = "brew.example.com"
	rec = httptest.NewRecorder()
	h.ServePrivateTapInfoRefs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-git-upload-pack-advertisement", rec.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "Authorization", rec.Header().Get("Vary"))
	assert.Contains(t, rec.Body.String(), "refs/heads/main")

	// The credentialed smart fetch works in place too.
	var body bytes.Buffer
	body.Write(pktLineString("want " + strings.Repeat("0", 40) + "\n")) // unknown want falls back to the scope's tip
	body.WriteString("0000")
	body.Write(pktLineString("done\n"))
	req = withReadToken(httptest.NewRequest("POST", "/private/tap.git/git-upload-pack", &body), nil)
	req.Host = "brew.example.com"
	rec = httptest.NewRecorder()
	h.ServePrivateTapUploadPack(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	assert.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte("0008NAK\nPACK")), "NAK + raw pack expected: %q", rec.Body.Bytes()[:min(rec.Body.Len(), 16)])
}
