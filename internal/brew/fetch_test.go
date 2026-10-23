package brew

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitTapRefreshes blocks until no lineage refresh is in flight.
func (h *Handler) waitTapRefreshes() {
	for {
		h.tapMu.Lock()
		var ref *tapRefresh
		for _, r := range h.tapRefreshes {
			ref = r
			break
		}
		h.tapMu.Unlock()
		if ref == nil {
			return
		}
		<-ref.done
	}
}

// uploadPack POSTs a raw upload-pack request body for the git.example.com tap.
func uploadPack(t *testing.T, h *Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/brew/tap.git/git-upload-pack", bytes.NewReader(body))
	req.Host = "git.example.com"
	rec := httptest.NewRecorder()
	h.ServeTapUploadPack(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	return rec
}

// packObjectCount reads the object count out of a v2 pack header.
func packObjectCount(t *testing.T, pack []byte) int {
	t.Helper()
	require.True(t, bytes.HasPrefix(pack, []byte("PACK")), "not a pack: %q", pack[:min(len(pack), 16)])
	require.GreaterOrEqual(t, len(pack), 12)
	return int(binary.BigEndian.Uint32(pack[8:12]))
}

// tapTip returns the tip the dumb info/refs advertises for git.example.com.
func tapTip(t *testing.T, h *Handler) string {
	t.Helper()
	rec := getTap(t, h, "git.example.com", "info/refs")
	require.Equal(t, http.StatusOK, rec.Code)
	return strings.Fields(rec.Body.String())[0]
}

// A brew update fetches with the tip it already has as a have. The server
// acknowledges it, ends the negotiation with "ready", and the pack carries only
// what the new commit adds. The commit, its root and Formula trees, and the new
// formula's blob. The full history is never sent again.
func TestServeUploadPack_FetchSendsOnlyMissingObjects(t *testing.T) {
	t.Serial()
	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	for _, name := range []string{"appone", "apptwo", "appthree"} {
		seedBrewProject(t, d, store, name, name+"-binary")
		tapTip(t, h)
	}
	oldTip := tapTip(t, h)
	seedBrewProject(t, d, store, "appfour", "appfour-binary")
	newTip := tapTip(t, h)
	require.NotEqual(t, oldTip, newTip)

	var round bytes.Buffer
	round.Write(pktLineString("want " + newTip + " multi_ack_detailed side-band-64k\n"))
	round.WriteString("0000")
	round.Write(pktLineString("have " + oldTip + "\n"))
	round.Write(pktLineString("have " + strings.Repeat("1", 40) + "\n"))
	round.WriteString("0000")
	var wantRound bytes.Buffer
	wantRound.Write(pktLineString("ACK " + oldTip + " common\n"))
	wantRound.Write(pktLineString("ACK " + oldTip + " ready\n"))
	wantRound.Write(pktLineString("NAK\n"))
	assert.Equal(t, wantRound.String(), uploadPack(t, h, round.Bytes()).Body.String())

	var final bytes.Buffer
	final.Write(pktLineString("want " + newTip + " multi_ack_detailed\n"))
	final.WriteString("0000")
	final.Write(pktLineString("have " + oldTip + "\n"))
	final.Write(pktLineString("done\n"))
	resp := uploadPack(t, h, final.Bytes()).Body.Bytes()
	ack := pktLineString("ACK " + oldTip + "\n")
	require.True(t, bytes.HasPrefix(resp, ack), "final response must ACK the common have: %q", resp[:min(len(resp), 64)])
	assert.Equal(t, 4, packObjectCount(t, resp[len(ack):]))

	var clone bytes.Buffer
	clone.Write(pktLineString("want " + newTip + " multi_ack_detailed\n"))
	clone.WriteString("0000")
	clone.Write(pktLineString("done\n"))
	full := uploadPack(t, h, clone.Bytes()).Body.Bytes()
	require.True(t, bytes.HasPrefix(full, []byte("0008NAK\n")))
	assert.Equal(t, 16, packObjectCount(t, full[len("0008NAK\n"):]), "a clone carries every commit")
}

// A real git client fetching after a publish gets an incremental pack that it
// accepts, and the result is a consistent repository.
func TestSmartFetch_IncrementalPackFastForwards(t *testing.T) {
	t.Serial()
	requireGit(t)
	oldTTL := tapCacheTTL
	tapCacheTTL = 0
	t.Cleanup(func() { tapCacheTTL = oldTTL })

	h, d, store := setupTest(t)
	for _, name := range []string{"appone", "apptwo", "appthree"} {
		seedBrewProject(t, d, store, name, name+"-binary")
		tapTip(t, h)
	}

	var mu sync.Mutex
	var lastPackObjects int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /brew/tap.git/info/refs", h.ServeTapInfoRefs)
	mux.HandleFunc("POST /brew/tap.git/git-upload-pack", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		h.ServeTapUploadPack(rec, r)
		body := rec.Body.Bytes()
		if i := bytes.Index(body, []byte("PACK")); i >= 0 && len(body) >= i+12 {
			mu.Lock()
			lastPackObjects = int(binary.BigEndian.Uint32(body[i+8 : i+12]))
			mu.Unlock()
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(body)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	dir := filepath.Join(gitScratchDir(t), "tap")
	runGit(t, gitScratchDir(t), "-c", "protocol.version=0", "clone", ts.URL+"/brew/tap.git", dir)
	oldTip := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	seedBrewProject(t, d, store, "appfour", "appfour-binary")
	runGit(t, dir, "-c", "protocol.version=0", "fetch", "--force", "origin")
	runGit(t, dir, "rebase", "origin/main")
	runGit(t, dir, "fsck", "--strict")

	newTip := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	require.NotEqual(t, oldTip, newTip)
	runGit(t, dir, "merge-base", "--is-ancestor", oldTip, newTip)
	assert.Equal(t, "Formula/appfour.rb\nFormula/appone.rb\nFormula/appthree.rb\nFormula/apptwo.rb\n",
		runGit(t, dir, "ls-tree", "-r", "--name-only", "HEAD"))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 4, lastPackObjects, "the fetch must carry only the new commit's objects")
}

// A refresh slower than tapRefreshWait never holds a request. The request
// serves the persisted tip, one refresh runs however many requests arrive, and
// the request after it finishes serves the new tip.
func TestTapRefresh_SlowRefreshServesPersistedTip(t *testing.T) {
	t.Serial()
	oldTTL, oldWait, oldRefresher := tapCacheTTL, tapRefreshWait, tapRefresher
	tapCacheTTL = 0
	tapRefreshWait = 20 * time.Millisecond
	t.Cleanup(func() { tapCacheTTL, tapRefreshWait, tapRefresher = oldTTL, oldWait, oldRefresher })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")
	tip1 := tapTip(t, h)

	release := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	tapRefresher = func(h *Handler, r *http.Request, dir string) error {
		calls.Add(1)
		<-release
		return oldRefresher(h, r, dir)
	}
	seedBrewProject(t, d, store, "apptwo", "apptwo-binary")

	for range 3 {
		start := time.Now()
		assert.Equal(t, tip1, tapTip(t, h), "a request during a slow refresh serves the persisted tip")
		assert.Less(t, time.Since(start), 5*time.Second)
	}
	assert.Equal(t, int32(1), calls.Load(), "requests during a refresh must not start another")

	dir := lineageDirFor(t, h)
	h.tapMu.Lock()
	ref := h.tapRefreshes[dir]
	h.tapMu.Unlock()
	require.NotNil(t, ref)
	unblock()
	<-ref.done
	require.NoError(t, ref.err)

	tapRefresher = oldRefresher
	tip2 := tapTip(t, h)
	assert.NotEqual(t, tip1, tip2)
	assert.Equal(t, tip1, readCommitParent(t, lineageDirFor(t, h), tip2))
}
