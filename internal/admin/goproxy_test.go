package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
