// Tests for the chunked OCI upload-session path (chunked.go): large-blob
// chunking, exact-multiple sizing, resume after transient failures, the
// no-progress abort, server-advertised limits, and Range parsing. Split from
// ociclient_test.go, which holds the fake registry and the single-request path.
package ociclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPush_LargeBlobChunked(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := make([]byte, 2500)
	for i := range layer {
		layer[i] = byte(i)
	}
	dir, _ := buildImageLayout(t, layer)

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest", "v1"}))

	assert.Equal(t, []int64{1000, 1000, 500}, f.patchSizes)
	assert.Equal(t, []string{"0-999", "1000-1999", "2000-2499"}, f.patchRange)
	assert.Equal(t, layer, f.blobs[digestOf(layer)], "chunks must reassemble byte-identically")
	assert.NotNil(t, f.manifests["latest"])
	assert.NotNil(t, f.manifests["v1"])
}

func TestPush_ChunkExactMultiple(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := make([]byte, 2000)
	dir, _ := buildImageLayout(t, layer)

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	assert.Equal(t, []int64{1000, 1000}, f.patchSizes, "an exact multiple must not send an empty trailing chunk")
	assert.Equal(t, layer, f.blobs[digestOf(layer)])
}

func TestPush_ResumesAfterTransientPatchFailure(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := make([]byte, 2500)
	for i := range layer {
		layer[i] = byte(i * 7)
	}
	dir, _ := buildImageLayout(t, layer)
	f.failPatches = 1

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	assert.Positive(t, f.statusReads, "a failed chunk must trigger a status read")
	assert.Equal(t, layer, f.blobs[digestOf(layer)], "resume must not corrupt the blob")
}

// A registry keeps upload sessions in memory, so a restart mid-push forgets
func TestPush_RestartsWhenTheRegistryForgetsTheSession(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := make([]byte, 2500)
	for i := range layer {
		layer[i] = byte(i * 3)
	}
	dir, _ := buildImageLayout(t, layer)
	f.dropSessionsAfter = 2

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	assert.Equal(t, layer, f.blobs[digestOf(layer)], "the restarted upload must reassemble byte-identically")
	assert.Equal(t, []int64{1000, 1000, 1000, 1000, 500}, f.patchSizes,
		"the two chunks sent before the session was dropped must be re-sent on the new one")
}

func TestPush_NoProgressAborts(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := make([]byte, 2500)
	dir, _ := buildImageLayout(t, layer)
	f.failPatches = 1000

	p := newPusher(srv, "proj", 1000)
	err := p.Push(dir, []string{"latest"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no progress")
}

func TestPush_ServerInfoLimitRespected(t *testing.T) {
	f := newFakeRegistry(t)
	mux := http.NewServeMux()
	mux.Handle("/v2/", f.handler("proj"))
	mux.HandleFunc("/api/v1/server-info", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"max_direct_upload_bytes":1000}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	layer := make([]byte, 2500)
	dir, _ := buildImageLayout(t, layer)

	p := newPusher(srv, "proj", 0) // no explicit chunk size: the server's limit decides
	p.Server = srv.URL
	require.NoError(t, p.Push(dir, []string{"latest"}))

	require.NotEmpty(t, f.patchSizes)
	for _, s := range f.patchSizes {
		assert.LessOrEqual(t, s, int64(1000), "no request may exceed the advertised limit")
	}
	assert.Equal(t, layer, f.blobs[digestOf(layer)])
}

func TestPush_ChunkSizeClampedToServerLimit(t *testing.T) {
	f := newFakeRegistry(t)
	mux := http.NewServeMux()
	mux.Handle("/v2/", f.handler("proj"))
	mux.HandleFunc("/api/v1/server-info", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"max_direct_upload_bytes":800}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	layer := make([]byte, 2000)
	dir, _ := buildImageLayout(t, layer)

	p := newPusher(srv, "proj", 100<<20) // way over the limit: must clamp down
	p.Server = srv.URL
	require.NoError(t, p.Push(dir, []string{"latest"}))

	require.NotEmpty(t, f.patchSizes)
	for _, s := range f.patchSizes {
		assert.LessOrEqual(t, s, int64(800))
	}
}

func TestCommittedFromRange(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0-0", 0, true},
		{"0-999", 1000, true},
		{"0-2499", 2500, true},
		{"", 0, false},
		{"1-999", 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := committedFromRange(c.in)
		assert.Equal(t, c.ok, ok, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}
