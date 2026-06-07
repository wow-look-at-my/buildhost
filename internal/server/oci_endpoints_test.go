package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOCI_V2Root_AuthDiscovery_FullStack drives the OCI auth-discovery handshake
// through the real server (auth middleware + router), reproducing the production
// bug: an unauthenticated GET /v2/ must answer 401 with a Basic challenge so the
// Docker/OCI client knows to send credentials. A 200 here makes clients conclude
// no auth is needed; the first real request then 401s and the pull dies.
func TestOCI_V2Root_AuthDiscovery_FullStack(t *testing.T) {
	env := setup(t)

	// Anonymous -> 401 + WWW-Authenticate challenge.
	resp := env.doSubdomainRequest(t, "GET", "oci", "/v2/", "", nil, false)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, `Basic realm="buildhost"`, resp.Header.Get("Www-Authenticate"))
	require.Equal(t, "registry/2.0", resp.Header.Get("Docker-Distribution-API-Version"))

	// HEAD behaves the same.
	head := env.doSubdomainRequest(t, "HEAD", "oci", "/v2/", "", nil, false)
	head.Body.Close()
	require.Equal(t, http.StatusUnauthorized, head.StatusCode)
	require.Equal(t, `Basic realm="buildhost"`, head.Header.Get("Www-Authenticate"))

	// Authenticated (the harness sends a valid Bearer token) -> 200, no challenge.
	ok := env.doSubdomainRequest(t, "GET", "oci", "/v2/", "", nil, true)
	defer ok.Body.Close()
	require.Equal(t, http.StatusOK, ok.StatusCode)
	require.Empty(t, ok.Header.Get("Www-Authenticate"))
}

// TestOCI_MultiArchPull_FullStack reproduces the dangling-index bug end to end:
// a synthesized multi-arch image serves an image index that lists per-platform
// child manifests by digest, and every one of those digests must be retrievable
// -- on both the manifests-by-digest and blobs-by-digest paths (the task
// confirmed both 404'd before the fix). The release is published through the
// real REST API and pulled through the real OCI endpoint.
func TestOCI_MultiArchPull_FullStack(t *testing.T) {
	env := setup(t)

	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects", `{"name":"multi","versioning":"auto","is_private":false}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.postJSON(t, "/api/v1/projects/multi/releases", `{"git_branch":"main"}`).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.putBody(t, "/api/v1/projects/multi/releases/1/artifacts/linux/amd64?kind=binary", []byte("#!/bin/sh\necho amd64\n")).StatusCode)
	require.Equal(t, http.StatusCreated,
		env.putBody(t, "/api/v1/projects/multi/releases/1/artifacts/linux/arm64?kind=binary", []byte("#!/bin/sh\necho arm64\n")).StatusCode)
	require.Equal(t, http.StatusOK,
		env.postJSON(t, "/api/v1/projects/multi/releases/1/publish", `{}`).StatusCode)

	// Pull the index by tag. Public project => anonymous read is allowed; the
	// /v2/ root still challenges (covered above), but the manifest read does not.
	resp := env.doSubdomainRequest(t, "GET", "oci", "/v2/multi/manifests/latest", "", nil, false)
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, "index pull: %s", body)
	require.Equal(t, "application/vnd.oci.image.index.v1+json", resp.Header.Get("Content-Type"))

	var index struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			Digest   string `json:"digest"`
			Size     int64  `json:"size"`
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	require.NoError(t, json.Unmarshal(body, &index))
	require.Len(t, index.Manifests, 2, "index must advertise both platform manifests")

	for _, child := range index.Manifests {
		assert.NotEmpty(t, child.Platform.Architecture)
		assert.Equal(t, "linux", child.Platform.OS)

		// Resolve the child via the manifests-by-digest path.
		mresp := env.doSubdomainRequest(t, "GET", "oci", "/v2/multi/manifests/"+child.Digest, "", nil, false)
		mbody := readBody(t, mresp)
		require.Equalf(t, http.StatusOK, mresp.StatusCode, "child manifest %s must resolve by digest: %s", child.Digest, mbody)
		assert.Equal(t, child.Digest, mresp.Header.Get("Docker-Content-Digest"))
		sum := sha256.Sum256(mbody)
		assert.Equal(t, child.Digest, "sha256:"+hex.EncodeToString(sum[:]), "served child must match advertised digest")

		// And via the blobs-by-digest path (clients try both; both 404'd before).
		bresp := env.doSubdomainRequest(t, "GET", "oci", "/v2/multi/blobs/"+child.Digest, "", nil, false)
		bresp.Body.Close()
		require.Equalf(t, http.StatusOK, bresp.StatusCode, "child %s must resolve as a blob too", child.Digest)

		// The child's own config + layer blobs must resolve as well.
		var cm struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
			Layers []struct {
				Digest string `json:"digest"`
			} `json:"layers"`
		}
		require.NoError(t, json.Unmarshal(mbody, &cm))
		blobs := append([]string{cm.Config.Digest}, layerDigests(cm.Layers)...)
		require.Len(t, blobs, 3, "config + essentials layer + binary layer")
		for _, dg := range blobs {
			r := env.doSubdomainRequest(t, "GET", "oci", "/v2/multi/blobs/"+dg, "", nil, false)
			r.Body.Close()
			require.Equalf(t, http.StatusOK, r.StatusCode, "blob %s referenced by child manifest must resolve", dg)
		}
	}
}

func layerDigests(layers []struct {
	Digest string `json:"digest"`
}) []string {
	out := make([]string, len(layers))
	for i, l := range layers {
		out[i] = l.Digest
	}
	return out
}
