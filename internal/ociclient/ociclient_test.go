package ociclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { RetryBaseDelay = time.Millisecond }

func TestPush_SmallBlobsMonolithic(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := []byte("small-layer-content")
	dir, manifestDigest := buildImageLayout(t, layer)

	p := newPusher(srv, "proj", 1<<20)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	assert.Empty(t, f.patchSizes, "small blobs must not open chunk sessions")
	assert.Equal(t, layer, f.blobs[digestOf(layer)])
	assert.NotNil(t, f.manifests["latest"])
	assert.Equal(t, f.manifests["latest"], f.manifests[manifestDigest], "tag and digest must resolve to the same manifest")
}

// TestPush_BuildxPerTagIndexEntries pushes a layout shaped the way
// `docker buildx --output type=oci` writes it for a MULTI-TAG build: one
// index.json entry per tag, all referencing the same digest with only the
// io.containerd.image.name annotation differing. (Regression: this shape was
// once rejected as "2 top-level manifests".)
func TestPush_BuildxPerTagIndexEntries(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := []byte("per-tag-layer")
	dir, manifestDigest := buildImageLayout(t, layer)

	// Rewrite index.json with two per-tag entries for the same manifest.
	entry := func(name string) map[string]any {
		return map[string]any{
			"mediaType":   "application/vnd.oci.image.manifest.v1+json",
			"digest":      manifestDigest,
			"size":        1, // descriptor size of the index entry is not used by the walk
			"annotations": map[string]any{"io.containerd.image.name": "reg.example/proj:" + name},
		}
	}
	// The walk reads the manifest blob by digest, so give the entries the real size.
	st, err := os.Stat(filepath.Join(dir, "blobs", "sha256", manifestDigest[len("sha256:"):]))
	require.NoError(t, err)
	e1, e2 := entry("sha"), entry("latest")
	e1["size"], e2["size"] = st.Size(), st.Size()
	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests":     []map[string]any{e1, e2},
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), index, 0o644))

	p := newPusher(srv, "proj", 1<<20)
	require.NoError(t, p.Push(dir, []string{"sha", "latest"}))
	assert.NotNil(t, f.manifests["sha"])
	assert.NotNil(t, f.manifests["latest"])
	assert.Equal(t, f.manifests["sha"], f.manifests[manifestDigest])
}

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

	// 2500 bytes at chunk 1000 -> 1000 + 1000 + 500.
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
	f.failPatches = 1 // first PATCH 500s after consuming the body

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	assert.Positive(t, f.statusReads, "a failed chunk must trigger a status read")
	assert.Equal(t, layer, f.blobs[digestOf(layer)], "resume must not corrupt the blob")
}

func TestPush_SkipsExistingBlobs(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := []byte("already-there")
	dir, _ := buildImageLayout(t, layer)
	f.blobs[digestOf(layer)] = layer

	p := newPusher(srv, "proj", 1<<20)
	require.NoError(t, p.Push(dir, []string{"latest"}))
	assert.NotContains(t, f.blobUploads, digestOf(layer), "an existing blob must not be re-uploaded")
	assert.NotEmpty(t, f.blobUploads, "the missing config blob must still upload")
}

func TestPush_NestedIndexWithAttestation(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	// Build a buildx-shaped layout: index.json -> nested index -> {image
	// manifest (platform), attestation manifest (unknown/unknown)}.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755))

	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	configDigest := writeBlob(t, dir, config)
	layer := make([]byte, 1500)
	layerDigest := writeBlob(t, dir, layer)
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configDigest, "size": len(config)},
		"layers":        []map[string]any{{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": layerDigest, "size": len(layer)}},
	})
	manifestDigest := writeBlob(t, dir, manifest)

	attConfig := []byte(`{"architecture":"unknown","os":"unknown"}`)
	attConfigDigest := writeBlob(t, dir, attConfig)
	attLayer := []byte(`{"predicateType":"https://slsa.dev/provenance"}`)
	attLayerDigest := writeBlob(t, dir, attLayer)
	attManifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": attConfigDigest, "size": len(attConfig)},
		"layers":        []map[string]any{{"mediaType": "application/vnd.in-toto+json", "digest": attLayerDigest, "size": len(attLayer)}},
	})
	attManifestDigest := writeBlob(t, dir, attManifest)

	nested, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": manifestDigest, "size": len(manifest),
				"platform": map[string]any{"architecture": "amd64", "os": "linux"}},
			{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": attManifestDigest, "size": len(attManifest),
				"platform":    map[string]any{"architecture": "unknown", "os": "unknown"},
				"annotations": map[string]any{"vnd.docker.reference.type": "attestation-manifest"}},
		},
	})
	nestedDigest := writeBlob(t, dir, nested)

	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{{
			"mediaType": "application/vnd.oci.image.index.v1+json",
			"digest":    nestedDigest, "size": len(nested),
		}},
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), index, 0o644))

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	// The tag is the nested index; both child manifests and all blobs arrived.
	assert.Equal(t, nested, f.manifests["latest"])
	assert.NotNil(t, f.manifests[manifestDigest])
	assert.NotNil(t, f.manifests[attManifestDigest])
	assert.Equal(t, layer, f.blobs[layerDigest])
	assert.Equal(t, attLayer, f.blobs[attLayerDigest])
}

func TestPush_NoProgressAborts(t *testing.T) {
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := make([]byte, 2500)
	dir, _ := buildImageLayout(t, layer)
	f.failPatches = 1000 // every PATCH 500s; status always reports 0

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

func TestParseRefs(t *testing.T) {
	reg, proj, tags, err := ParseRefs([]string{
		"oci.pazer.build/playwright-multiplexer/playwright-mcp:abc123",
		"oci.pazer.build/playwright-multiplexer/playwright-mcp:latest",
		"oci.pazer.build/playwright-multiplexer/playwright-mcp",
	})
	require.NoError(t, err)
	assert.Equal(t, "oci.pazer.build", reg)
	assert.Equal(t, "playwright-multiplexer/playwright-mcp", proj)
	assert.Equal(t, []string{"abc123", "latest", "latest"}, tags)

	_, _, _, err = ParseRefs([]string{"oci.a.example/p:1", "oci.b.example/p:1"})
	assert.ErrorContains(t, err, "same registry")

	_, _, _, err = ParseRefs([]string{"oci.a.example/p:1", "oci.a.example/other:1"})
	assert.ErrorContains(t, err, "same registry")

	_, _, _, err = ParseRefs([]string{"noregistry:tag"})
	assert.ErrorContains(t, err, "registry host")

	_, _, _, err = ParseRefs(nil)
	assert.Error(t, err)

	// Port in the registry host, tag after the last colon.
	reg, proj, tags, err = ParseRefs([]string{"localhost:8080/proj:v1"})
	require.NoError(t, err)
	assert.Equal(t, "localhost:8080", reg)
	assert.Equal(t, "proj", proj)
	assert.Equal(t, []string{"v1"}, tags)
}

func TestDeriveServer(t *testing.T) {
	assert.Equal(t, "https://pazer.build", DeriveServer("oci.pazer.build", false))
	assert.Equal(t, "http://pazer.build", DeriveServer("oci.pazer.build", true))
	assert.Equal(t, "https://example.com", DeriveServer("docker.example.com", false))
	assert.Equal(t, "", DeriveServer("registry.example.com", false))
	assert.Equal(t, "http://localhost:8080", DeriveServer("oci.localhost:8080", true))
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
