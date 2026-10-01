package ociclient

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { RetryBaseDelay = time.Millisecond }

func TestPush_SmallBlobsFinalizeInOneRequest(t *testing.T) {
	t.Serial()
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

// Every image built FROM a published base carries that base's layers, and the
// registry already stores them. Asking to mount before uploading is what keeps
func TestPush_MountsBlobsTheRegistryAlreadyStores(t *testing.T) {
	t.Serial()
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	base := make([]byte, 4000) // big enough that uploading it would chunk
	for i := range base {
		base[i] = byte(i)
	}
	own := []byte("this-image-only")
	dir, _ := buildImageLayout(t, base, own)
	f.mountable[digestOf(base)] = true

	p := newPusher(srv, "proj", 1000)
	require.NoError(t, p.Push(dir, []string{"latest"}))

	assert.Equal(t, []string{digestOf(base)}, f.mounts)
	assert.NotContains(t, f.blobUploads, digestOf(base), "a mounted blob must not be uploaded")
	assert.Empty(t, f.patchSizes, "a mounted blob must not open a chunk session")
	assert.Contains(t, f.blobUploads, digestOf(own), "the layer only this image has still uploads")
	assert.NotNil(t, f.manifests["latest"])
}

// TestPush_BuildxPerTagIndexEntries pushes a layout shaped the way
func TestPush_BuildxPerTagIndexEntries(t *testing.T) {
	t.Serial()
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	layer := []byte("per-tag-layer")
	dir, manifestDigest := buildImageLayout(t, layer)

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

func TestPush_SkipsExistingBlobs(t *testing.T) {
	t.Serial()
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
	t.Serial()
	f := newFakeRegistry(t)
	srv := httptest.NewServer(f.handler("proj"))
	defer srv.Close()

	// Build a buildx-shaped layout: index.json -> nested index -> {image
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

func TestParseRefs(t *testing.T) {
	t.Serial()
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
	t.Serial()
	assert.Equal(t, "https://pazer.build", DeriveServer("oci.pazer.build", false))
	assert.Equal(t, "http://pazer.build", DeriveServer("oci.pazer.build", true))
	assert.Equal(t, "https://example.com", DeriveServer("docker.example.com", false))
	assert.Equal(t, "", DeriveServer("registry.example.com", false))
	assert.Equal(t, "http://localhost:8080", DeriveServer("oci.localhost:8080", true))
}
