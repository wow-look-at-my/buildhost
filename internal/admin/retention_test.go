package admin

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminRetention_GetDefaults(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := serve(srv, "GET", "/api/retention", nil)
	require.Equal(t, 200, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, float64(10), got["keep_n"])
	assert.Equal(t, float64(24), got["recency_hours"])
	assert.Equal(t, false, got["sweeper_enabled"])
	require.NotNil(t, got["preview"])
	preview := got["preview"].(map[string]any)
	assert.Contains(t, preview, "reclaimable_bytes")
	assert.Contains(t, preview, "releases")
}

func TestAdminRetention_Update(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := serve(srv, "PUT", "/api/retention", bytes.NewBufferString(`{"keep_n":3,"recency_hours":6}`))
	require.Equal(t, 200, rec.Code)

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, float64(3), got["keep_n"])
	assert.Equal(t, float64(6), got["recency_hours"])

	// Persisted across requests.
	rec = serve(srv, "GET", "/api/retention", nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, float64(3), got["keep_n"])
}

func TestAdminRetention_UpdateValidation(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, body := range []string{
		`{"keep_n":-1,"recency_hours":6}`,     // negative keep_n
		`{"keep_n":3,"recency_hours":-1}`,     // negative recency
		`{"keep_n":200000,"recency_hours":6}`, // keep_n too large
		`{"recency_hours":6}`,                 // missing keep_n
		`{"keep_n":3}`,                        // missing recency_hours
		`not json`,                            // malformed
	} {
		rec := serve(srv, "PUT", "/api/retention", bytes.NewBufferString(body))
		assert.Equal(t, 400, rec.Code, "expected 400 for body %q", body)
	}
}

func TestAdminRetention_Run(t *testing.T) {
	srv, database := newTestServer(t)
	seedData(t, database)

	rec := serve(srv, "POST", "/api/retention/run", bytes.NewBufferString(`{"enforce":false}`))
	require.Equal(t, 200, rec.Code)

	var rep map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep))
	assert.Equal(t, false, rep["enforced"])
	assert.Contains(t, rep, "reclaimable_bytes")
	assert.Contains(t, rep, "release_count")
}
