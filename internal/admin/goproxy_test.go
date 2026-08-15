package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server whose goproxy backend is not running must say so rather than 500 or
// render an empty dashboard that looks like a proxy with nothing cached. The
// admin package does not import the backend's registration, so this is the
// state under test here; the populated shape is covered in internal/goproxy's
// own Snapshot tests.
func TestAPIGoproxy_NotRunning(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodGet, "/api/goproxy", nil)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["enabled"])
	assert.NotContains(t, resp, "state")
}

func TestAPIGoproxyRecheck_NotRunning(t *testing.T) {
	srv, _ := newTestServer(t)

	w := serve(srv, http.MethodPost, "/api/goproxy/recheck", nil)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
