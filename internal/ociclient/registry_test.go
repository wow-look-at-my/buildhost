package ociclient

// Test harness: a minimal in-memory fake of the buildhost OCI registry (blob
// upload sessions, mounts, manifest PUTs) plus helpers that build on-disk OCI

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

	"github.com/stretchr/testify/require"
)

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
	blobUploads []string // digests that arrived as uploaded bytes, not a mount

	failPatches int
	// statusReads counts GET upload-status requests (the resume path).
	statusReads int
	// mountable is the set of digests this registry will grant a cross-repo
	mountable map[string]bool
	// mounts records the digests that were mounted rather than uploaded.
	mounts            []string
	dropSessionsAfter int
	patchCount        int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	return &fakeRegistry{
		t:         t,
		blobs:     map[string][]byte{},
		manifests: map[string][]byte{},
		sessions:  map[string][]byte{},
		mountable: map[string]bool{},
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
			if mount := r.URL.Query().Get("mount"); mount != "" && f.mountable[mount] {
				f.blobs[mount] = nil // linked, not uploaded
				f.mounts = append(f.mounts, mount)
				w.Header().Set("Docker-Content-Digest", mount)
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
			f.patchCount++
			if f.dropSessionsAfter > 0 && f.patchCount >= f.dropSessionsAfter {
				// A restart takes every in-memory session with it.
				f.dropSessionsAfter = 0
				clear(f.sessions)
				w.WriteHeader(http.StatusNotFound)
				return
			}
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
			// The real server appends the PUT body before finalizing, which is
			sess = append(sess, readAll(f.t, r)...)
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
