package server_test

// End-to-end WebAssembly artifact flow: upload a wasm module via the artifact
// PUT (os=wasm, arch=js/wasip1), publish, then download it back through the
// dl endpoint and the static endpoint, asserting the bytes round-trip.

import (
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWasmArtifact_UploadDownloadRoundTrip(t *testing.T) {
	env := setup(t)

	// A fake wasm module (real magic bytes, fake contents).
	jsPayload := []byte("\x00asm\x01\x00\x00\x00js-flavor-module")
	wasip1Payload := []byte("\x00asm\x01\x00\x00\x00wasip1-flavor-module")

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"wasmapp","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/wasmapp/releases", `{"git_branch":"master","git_commit":"abc123"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Upload one artifact per Go wasm port.
	resp = env.putBody(t, "/api/v1/projects/wasmapp/releases/1/artifacts/wasm/js", jsPayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.putBody(t, "/api/v1/projects/wasmapp/releases/1/artifacts/wasm/wasip1", wasip1Payload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// An incompatible pair is rejected end to end (wasm never rides the
	// native amd64/arm64 arches).
	resp = env.putBody(t, "/api/v1/projects/wasmapp/releases/1/artifacts/wasm/amd64", jsPayload)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/wasmapp/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	for _, tc := range []struct {
		arch    string
		payload []byte
	}{
		{"js", jsPayload},
		{"wasip1", wasip1Payload},
	} {
		// dl redirects to static with the canonical os/arch.
		resp = env.getSubdomain(t, "dl", "/wasmapp?v=1&os=wasm&arch="+tc.arch)
		require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		require.Contains(t, loc, "static.test.local/file?")
		require.Contains(t, loc, "os=wasm")
		require.Contains(t, loc, "arch="+tc.arch)

		// Following the redirect serves the exact uploaded bytes.
		locURL, err := url.Parse(loc)
		require.NoError(t, err)
		resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, tc.payload, body, "arch=%s bytes must round-trip", tc.arch)
	}

	// "latest" resolution works for wasm like any platform.
	resp = env.getSubdomain(t, "dl", "/wasmapp?os=wasm&arch=js")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "os=wasm")
	resp.Body.Close()
}
