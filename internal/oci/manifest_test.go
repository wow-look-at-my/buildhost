package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// TestServeHTTP_MultiArchIndex_ChildrenResolveByDigest is the regression test
// for the dangling-index bug: a synthesized multi-arch image serves an image
// index that lists per-platform child manifests by digest, and every one of
// those digests MUST be retrievable -- both as a manifest and (its config +
// layers) as blobs. Before the fix the children were generated only to compute
// the index and never stored, so fetching a child digest 404'd and no client
// could pull a platform image.
func TestServeHTTP_MultiArchIndex_ChildrenResolveByDigest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishMultiArch(t, ctx, d, store, proj, "1.0.0", 1000000)

	// Fetch the index by tag.
	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/vnd.oci.image.index.v1+json", rec.Header().Get("Content-Type"))

	var index struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
			Platform  struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &index))
	require.Len(t, index.Manifests, 2, "index must list both platform manifests")

	for _, child := range index.Manifests {
		require.True(t, validDigest.MatchString(child.Digest), "child digest %q malformed", child.Digest)
		assert.NotEmpty(t, child.Platform.Architecture)
		assert.Equal(t, "linux", child.Platform.OS)

		// The child manifest must resolve by digest.
		mreq := httptest.NewRequest("GET", "/v2/myapp/manifests/"+child.Digest, nil)
		mreq = withRoute(mreq, proj, route{project: "myapp", action: "manifests", reference: child.Digest})
		mrec := httptest.NewRecorder()
		h.ServeHTTP(mrec, mreq)
		require.Equal(t, http.StatusOK, mrec.Code, "child manifest %s must resolve by digest", child.Digest)
		assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", mrec.Header().Get("Content-Type"))
		assert.Equal(t, child.Digest, mrec.Header().Get("Docker-Content-Digest"))

		// The bytes served must hash to exactly the advertised digest and size.
		sum := sha256.Sum256(mrec.Body.Bytes())
		assert.Equal(t, child.Digest, "sha256:"+hex.EncodeToString(sum[:]), "served child must match its index digest")
		assert.Equal(t, child.Size, int64(mrec.Body.Len()), "served child size must match the index")

		// Its config + layer blobs must also resolve by digest.
		var cm struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
			Layers []struct {
				Digest string `json:"digest"`
			} `json:"layers"`
		}
		require.NoError(t, json.Unmarshal(mrec.Body.Bytes(), &cm))
		blobDigests := []string{cm.Config.Digest}
		for _, l := range cm.Layers {
			blobDigests = append(blobDigests, l.Digest)
		}
		require.Len(t, blobDigests, 3, "config + two layers")
		for _, dg := range blobDigests {
			breq := httptest.NewRequest("GET", "/v2/myapp/blobs/"+dg, nil)
			breq = withRoute(breq, proj, route{project: "myapp", action: "blobs", reference: dg})
			brec := httptest.NewRecorder()
			h.ServeHTTP(brec, breq)
			assert.Equal(t, http.StatusOK, brec.Code, "blob %s referenced by child manifest must resolve", dg)
		}
	}
}

// TestServeHTTP_MultiArchIndex_IndexResolvesByDigest is the regression test for
// the by-digest *index* bug: a synthesized multi-arch image serves its image
// index by tag and advertises the index's own content digest in Docker-Content-
// Digest, and that digest MUST itself be retrievable. The Docker daemon's classic
// (overlay2 / non-containerd) image store pulls a tag by reading the manifest,
// then re-requests the *same* manifest by the advertised digest to store it
// content-addressably; for the parent index that by-digest GET previously 404'd
// (the index was served by tag but, unlike its children, never persisted), so
// `docker pull <repo>:<tag>` failed with "manifest unknown" even though pulling a
// child platform image by digest worked.
func TestServeHTTP_MultiArchIndex_IndexResolvesByDigest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishMultiArch(t, ctx, d, store, proj, "1.0.0", 1000000)

	// Fetch the index by tag (this is the request that synthesizes and now
	// persists the index) and capture the digest it advertises.
	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/vnd.oci.image.index.v1+json", rec.Header().Get("Content-Type"))

	byTag := append([]byte(nil), rec.Body.Bytes()...)
	indexDigest := rec.Header().Get("Docker-Content-Digest")
	require.True(t, validDigest.MatchString(indexDigest), "index digest %q malformed", indexDigest)
	// The advertised digest must be the hash of the exact body served (canonical).
	sum := sha256.Sum256(byTag)
	require.Equal(t, indexDigest, "sha256:"+hex.EncodeToString(sum[:]), "advertised digest must match the index body")

	// The index by its own digest must now resolve (was 404 before the fix), with
	// the index media type and byte-identical content.
	dreq := httptest.NewRequest("GET", "/v2/myapp/manifests/"+indexDigest, nil)
	dreq = withRoute(dreq, proj, route{project: "myapp", action: "manifests", reference: indexDigest})
	drec := httptest.NewRecorder()
	h.ServeHTTP(drec, dreq)
	require.Equal(t, http.StatusOK, drec.Code, "index must resolve by its own digest")
	assert.Equal(t, "application/vnd.oci.image.index.v1+json", drec.Header().Get("Content-Type"))
	assert.Equal(t, indexDigest, drec.Header().Get("Docker-Content-Digest"))
	assert.Equal(t, byTag, drec.Body.Bytes(), "index served by digest must be byte-identical to the one served by tag")

	// HEAD by digest must also resolve and advertise the same digest (the daemon
	// may HEAD a manifest before GETting it).
	hreq := httptest.NewRequest("HEAD", "/v2/myapp/manifests/"+indexDigest, nil)
	hreq = withRoute(hreq, proj, route{project: "myapp", action: "manifests", reference: indexDigest})
	hrec := httptest.NewRecorder()
	h.ServeHTTP(hrec, hreq)
	require.Equal(t, http.StatusOK, hrec.Code, "index must resolve by digest via HEAD")
	assert.Equal(t, indexDigest, hrec.Header().Get("Docker-Content-Digest"))
}

func TestBlobsReachableFromManifest(t *testing.T) {
	h, d, store := setupTest(t)
	ctx := context.Background()

	proj := &db.Project{Name: "myapp", Versioning: db.VersioningSemver}
	require.NoError(t, d.CreateProject(ctx, proj))
	publishWithOCI(t, ctx, d, store, proj, "1.0.0", 1000000)

	// Get manifest
	req := httptest.NewRequest("GET", "/v2/myapp/manifests/latest", nil)
	req = withRoute(req, proj, route{project: "myapp", action: "manifests", reference: "latest"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &manifest))
	// Base (essentials) layer + per-binary layer; both must be reachable below.
	require.Len(t, manifest.Layers, 2)

	// Fetch config blob
	req = httptest.NewRequest("GET", "/v2/myapp/blobs/"+manifest.Config.Digest, nil)
	req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: manifest.Config.Digest})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var config map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &config))
	assert.Equal(t, "amd64", config["architecture"])
	assert.Equal(t, "linux", config["os"])

	// Fetch layer blob
	for _, layer := range manifest.Layers {
		req = httptest.NewRequest("GET", "/v2/myapp/blobs/"+layer.Digest, nil)
		req = withRoute(req, proj, route{project: "myapp", action: "blobs", reference: layer.Digest})
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEmpty(t, rec.Body.Bytes())
	}
}
