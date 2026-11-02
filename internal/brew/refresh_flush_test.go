package brew

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errRefreshBroken = errors.New("refresh broken")

// failingRefresher fails every refresh and counts the calls.
func failingRefresher(calls *atomic.Int32) func(*Handler, *http.Request, string) error {
	return func(*Handler, *http.Request, string) error {
		calls.Add(1)
		return errRefreshBroken
	}
}

func TestTapRefresh_FailureWithoutTipIsAnError(t *testing.T) {
	t.Serial()
	oldTTL, oldRefresher := tapCacheTTL, tapRefresher
	tapCacheTTL = time.Hour
	t.Cleanup(func() { tapCacheTTL, tapRefresher = oldTTL, oldRefresher })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")

	var calls atomic.Int32
	tapRefresher = failingRefresher(&calls)
	for range 3 {
		assert.Equal(t, http.StatusInternalServerError, getTap(t, h, "git.example.com", "info/refs").Code)
	}
	assert.Equal(t, int32(1), calls.Load(), "a failed refresh is retried once per tapCacheTTL, not per request")

	tapRefresher = oldRefresher
	tapCacheTTL = 0
	assert.Equal(t, http.StatusOK, getTap(t, h, "git.example.com", "info/refs").Code)
}

// It never serves the tip as though the refresh had worked. The failure
// stays an error until a later refresh succeeds.
func TestTapRefresh_FailureWithTipIsAnError(t *testing.T) {
	t.Serial()
	oldTTL, oldRefresher := tapCacheTTL, tapRefresher
	tapCacheTTL = time.Hour
	t.Cleanup(func() { tapCacheTTL, tapRefresher = oldTTL, oldRefresher })

	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")
	tip := tapTip(t, h)

	var calls atomic.Int32
	tapRefresher = failingRefresher(&calls)
	tapCacheTTL = 0
	assert.Equal(t, http.StatusInternalServerError, getTap(t, h, "git.example.com", "info/refs").Code)
	assert.Equal(t, int32(1), calls.Load())

	tapCacheTTL = time.Hour
	for range 3 {
		assert.Equal(t, http.StatusInternalServerError, getTap(t, h, "git.example.com", "info/refs").Code)
	}
	assert.Equal(t, int32(1), calls.Load(), "a failed refresh is retried once per tapCacheTTL, not per request")

	tapRefresher = oldRefresher
	tapCacheTTL = 0
	assert.Equal(t, tip, tapTip(t, h))
}

// The upload-pack response's headers and its ACK/NAK reach a real client
// while the pack is still being assembled. A proxy in front therefore sees a
// first byte at once, however long the pack takes.
func TestServeUploadPack_FlushesBeforePackAssembly(t *testing.T) {
	t.Serial()
	h, d, store := setupTest(t)
	seedBrewProject(t, d, store, "appone", "appone-binary")
	tip := tapTip(t, h)
	ts := smartTapServer(t, h)

	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	oldEntries := tapPackEntries
	t.Cleanup(func() { tapPackEntries = oldEntries })
	tapPackEntries = func(root *os.Root, commits []commitNode, boundary []string) ([]string, error) {
		<-release
		return oldEntries(root, commits, boundary)
	}

	var body bytes.Buffer
	body.Write(pktLineString("want " + tip + " multi_ack_detailed side-band-64k\n"))
	body.WriteString("0000")
	body.Write(pktLineString("done\n"))

	type result struct {
		resp *http.Response
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/brew/tap.git/git-upload-pack", "application/x-git-upload-pack-request", &body)
		got <- result{resp, err}
	}()

	var res result
	select {
	case res = <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("no response headers while the pack was being assembled")
	}
	require.NoError(t, res.err)
	defer res.resp.Body.Close()
	assert.Equal(t, http.StatusOK, res.resp.StatusCode)
	assert.Equal(t, "application/x-git-upload-pack-result", res.resp.Header.Get("Content-Type"))

	first := make([]byte, len("0008NAK\n"))
	_, err := io.ReadFull(res.resp.Body, first)
	require.NoError(t, err)
	assert.Equal(t, "0008NAK\n", string(first))

	unblock()
	rest, err := io.ReadAll(res.resp.Body)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(rest), "PACK"), "the pack follows once assembly finishes")
}
