package ociclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { RetryBaseDelay = time.Millisecond }

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// writeBlob writes content into a layout dir's blobs/sha256 and returns its digest.
func writeBlob(t *testing.T, dir string, content []byte) string {
	t.Helper()
	d := digestOf(content)
	p := filepath.Join(dir, "blobs", "sha256", d[len("sha256:"):])
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, content, 0o644))
	return d
}

// buildImageLayout creates an OCI layout dir: config + layers -> image
// manifest -> index.json. Returns the layout dir and the manifest digest.
func buildImageLayout(t *testing.T, layers ...[]byte) (string, string) {
	t.Helper()
	dir := t.TempDir()

	config := []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers"}}`)
	configDigest := writeBlob(t, dir, config)

	var layerDescs []map[string]any
	for _, l := range layers {
		d := writeBlob(t, dir, l)
		layerDescs = append(layerDescs, map[string]any{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    d, "size": len(l),
		})
	}
	manifest, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    configDigest, "size": len(config),
		},
		"layers": layerDescs,
	})
	manifestDigest := writeBlob(t, dir, manifest)

	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest":    manifestDigest, "size": len(manifest),
		}},
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), index, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644))
	return dir, manifestDigest
}

// fakeRegistry is an in-memory OCI registry implementing exactly the endpoints
// the pusher uses, with per-request instrumentation for the tests.
type fakeRegistry struct {
	t  *testing.T
	mu sync.Mutex

	blobs     map[string][]byte // digest -> content
	manifests map[string][]byte // reference -> content
	sessions  map[string][]byte // uuid -> accumulated bytes
	nextSess  int

	patchSizes  []int64 // observed PATCH body sizes
	patchRange  []string
	blobUploads []string // digests that arrived via upload (monolithic or finalize)

	// failPatches makes the next N PATCH requests 500 AFTER consuming the body.
	failPatches int
	// statusReads counts GET upload-status requests (the resume path).
	statusReads int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	return &fakeRegistry{
		t:         t,
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		sessions:  map[string][]byte{},
	}
}

func (f *fakeRegistry) handler(project string) http.Handler {
	prefix := "/v2/" + project + "/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, prefix)
		switch {
		case r.Method == "HEAD" && strings.HasPrefix(path, "blobs/"):
			if _, ok := f.blobs[strings.TrimPrefix(path, "blobs/")]; ok {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == "POST" && path == "blobs/uploads/":
			if digest := r.URL.Query().Get("digest"); digest != "" {
				body := readAll(f.t, r)
				if digestOf(body) != digest {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.blobs[digest] = body
				f.blobUploads = append(f.blobUploads, digest)
				w.WriteHeader(http.StatusCreated)
				return
			}
			f.nextSess++
			uuid := "sess-" + strconv.Itoa(f.nextSess)
			f.sessions[uuid] = []byte{}
			w.Header().Set("Location", "/v2/"+project+"/blobs/uploads/"+uuid)
			w.Header().Set("Docker-Upload-UUID", uuid)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == "PATCH" && strings.HasPrefix(path, "blobs/uploads/"):
			uuid := strings.TrimPrefix(path, "blobs/uploads/")
			sess, ok := f.sessions[uuid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			body := readAll(f.t, r)
			if f.failPatches > 0 {
				f.failPatches--
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			cr := r.Header.Get("Content-Range")
			f.patchRange = append(f.patchRange, cr)
			start, _ := strconv.ParseInt(strings.SplitN(cr, "-", 2)[0], 10, 64)
			if start != int64(len(sess)) {
				w.Header().Set("Range", fmt.Sprintf("0-%d", max(int64(len(sess))-1, 0)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			f.patchSizes = append(f.patchSizes, int64(len(body)))
			f.sessions[uuid] = append(sess, body...)
			w.Header().Set("Range", fmt.Sprintf("0-%d", max(int64(len(f.sessions[uuid]))-1, 0)))
			w.WriteHeader(http.StatusAccepted)
		case r.Method == "GET" && strings.HasPrefix(path, "blobs/uploads/"):
			f.statusReads++
			uuid := strings.TrimPrefix(path, "blobs/uploads/")
			sess, ok := f.sessions[uuid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Range", fmt.Sprintf("0-%d", max(int64(len(sess))-1, 0)))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "PUT" && strings.HasPrefix(path, "blobs/uploads/"):
			uuid := strings.TrimPrefix(path, "blobs/uploads/")
			sess, ok := f.sessions[uuid]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			digest := r.URL.Query().Get("digest")
			if digestOf(sess) != digest {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "digest mismatch: got %s want %s", digestOf(sess), digest)
				return
			}
			f.blobs[digest] = sess
			f.blobUploads = append(f.blobUploads, digest)
			delete(f.sessions, uuid)
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusCreated)
		case r.Method == "PUT" && strings.HasPrefix(path, "manifests/"):
			ref := strings.TrimPrefix(path, "manifests/")
			body := readAll(f.t, r)
			// Mirror the real server: every referenced blob/manifest must
			// already be present.
			var m imageManifest
			require.NoError(f.t, json.Unmarshal(body, &m))
			refs := m.Manifests
			refs = append(refs, m.Layers...)
			if m.Config != nil {
				refs = append(refs, *m.Config)
			}
			for _, d := range refs {
				_, blobOK := f.blobs[d.Digest]
				_, manOK := f.manifests[d.Digest]
				if !blobOK && !manOK {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, "referenced %s not pushed", d.Digest)
					return
				}
			}
			f.manifests[ref] = body
			f.manifests[digestOf(body)] = body
			w.Header().Set("Docker-Content-Digest", digestOf(body))
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func readAll(t *testing.T, r *http.Request) []byte {
	t.Helper()
	b := make([]byte, 0, 1024)
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Body.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			return b
		}
	}
}

func newPusher(srv *httptest.Server, project string, chunk int64) *Pusher {
	return &Pusher{
		Registry:  strings.TrimPrefix(srv.URL, "http://"),
		Project:   project,
		Token:     "test-token",
		PlainHTTP: true,
		ChunkSize: chunk,
	}
}

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
