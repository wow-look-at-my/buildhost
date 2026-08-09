// Tests for newRequest (upload.go): every upload body the pusher sends must
// carry a GetBody, or net/http declines to retry it after a mid-flight stream
// error and one hiccup fails the publish.
package ociclient

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readAllFromGetBody replays the request body the way the transport does.
func readAllFromGetBody(t *testing.T, req *http.Request) string {
	t.Helper()
	require.NotNil(t, req.GetBody, "no GetBody: the transport cannot retry this request")
	rc, err := req.GetBody()
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(b)
}

func TestNewRequestRewindsAFileBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blob")
	require.NoError(t, os.WriteFile(path, []byte("layer-bytes"), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	req, err := newRequest(http.MethodPut, "https://registry.example/v2/p/blobs/uploads/u", f, 11)
	require.NoError(t, err)
	assert.Equal(t, int64(11), req.ContentLength)

	// Drain it once, as a first attempt would, then prove a replay still sees
	// the whole blob rather than nothing.
	_, err = io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "layer-bytes", readAllFromGetBody(t, req))
	assert.Equal(t, "layer-bytes", readAllFromGetBody(t, req))
}

// A chunk is a section reader anchored partway into the blob, and a replay has
// to return that chunk -- not the start of the file.
func TestNewRequestRewindsAChunkToItsOwnStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blob")
	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	chunk := io.NewSectionReader(f, 4, 3)
	req, err := newRequest(http.MethodPatch, "https://registry.example/v2/p/blobs/uploads/u", chunk, 3)
	require.NoError(t, err)

	_, err = io.ReadAll(chunk)
	require.NoError(t, err)
	assert.Equal(t, "456", readAllFromGetBody(t, req))
}

func TestNewRequestLeavesABodylessRequestAlone(t *testing.T) {
	req, err := newRequest(http.MethodHead, "https://registry.example/v2/p/blobs/sha256:abc", nil, 0)
	require.NoError(t, err)
	assert.Empty(t, req.Header.Get("Content-Type"))
	assert.Nil(t, req.Body)
}
