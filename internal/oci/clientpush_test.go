package oci

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/ociclient"
)

// newLiveRegistry serves the real OCI handler over HTTP, simulating the auth
// middleware's context injection -- so the CLI push client is exercised against
// the actual server implementation, chunk protocol and all.
func newLiveRegistry(h *Handler, proj *db.Project) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt := parseOCIPath(strings.TrimPrefix(r.URL.Path, "/v2/"))
		rt.method = r.Method
		h.ServeHTTP(w, withRoute(r, proj, rt))
	}))
}

// writeLayoutBlob writes content into layoutDir's blob store, returning digest.
func writeLayoutBlob(t *testing.T, dir string, content []byte) string {
	t.Helper()
	d := digestOf(content)
	p := filepath.Join(dir, "blobs", "sha256", d[len("sha256:"):])
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, content, 0o644))
	return d
}

// TestClientPush_EndToEnd pushes a synthetic image through the real handler
// with a tiny chunk size, then pulls everything back through the real serve
// paths -- the full client/server chunk protocol round trip.
func TestClientPush_EndToEnd(t *testing.T) {
	h, d, _ := setupTest(t)
	ctx := t.Context()
	proj := &db.Project{Name: "pwmux/mcp", Versioning: db.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))
	srv := newLiveRegistry(h, proj)
	defer srv.Close()

	// A layer big enough to need several chunks at the test chunk size.
	layer := make([]byte, 10_000)
	for i := range layer {
		layer[i] = byte(i % 251)
	}
	config := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers"}}`)

	dir := t.TempDir()
	configDigest := writeLayoutBlob(t, dir, config)
	layerDigest := writeLayoutBlob(t, dir, layer)
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaImageManifest,
		"config":        map[string]any{"mediaType": mediaImageConfig, "digest": configDigest, "size": len(config)},
		"layers":        []map[string]any{{"mediaType": mediaImageLayer, "digest": layerDigest, "size": len(layer)}},
	})
	manifestDigest := writeLayoutBlob(t, dir, manifest)
	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     mediaImageIndex,
		"manifests": []map[string]any{{
			"mediaType": mediaImageManifest, "digest": manifestDigest, "size": len(manifest),
		}},
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), index, 0o644))

	p := &ociclient.Pusher{
		Registry:  strings.TrimPrefix(srv.URL, "http://"),
		Project:   proj.Name,
		Token:     "unused-in-test",
		PlainHTTP: true,
		ChunkSize: 1024, // 10 chunks: exercises the sequential-append protocol
	}
	require.NoError(t, p.Push(dir, []string{"latest", "abc123"}))

	// The tag resolves to the pushed manifest.
	rec := getManifest(t, h, proj, "latest")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, manifestDigest, rec.Header().Get("Docker-Content-Digest"))
	assert.JSONEq(t, string(manifest), rec.Body.String())

	// The chunked layer round-trips byte-identically.
	req := httptest.NewRequest("GET", "/v2/"+proj.Name+"/blobs/"+layerDigest, nil)
	req = withRoute(req, proj, route{project: proj.Name, action: "blobs", reference: layerDigest})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, layer, rec.Body.Bytes())

	// Both tags exist and a docker release was recorded.
	tag, err := d.GetOCITag(ctx, proj.ID, "latest")
	require.NoError(t, err)
	assert.Equal(t, manifestDigest, tag.ManifestDigest)
	tag2, err := d.GetOCITag(ctx, proj.ID, "abc123")
	require.NoError(t, err)
	assert.Equal(t, tag.ReleaseID, tag2.ReleaseID, "both tags must share one release")

	// Re-pushing the identical image is an idempotent no-op.
	require.NoError(t, p.Push(dir, []string{"latest"}))
}
