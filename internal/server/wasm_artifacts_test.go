package server_test

// End-to-end WebAssembly artifact flow: upload a wasm module via the artifact
// PUT (os=wasm, arch=js/wasip1), publish, then download it back through the

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestWasmArtifact_UploadDownloadRoundTrip(t *testing.T) {
	t.Serial()
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

	resp = env.putBody(t, "/api/v1/projects/wasmapp/releases/1/artifacts/wasm/js", jsPayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.putBody(t, "/api/v1/projects/wasmapp/releases/1/artifacts/wasm/wasip1", wasip1Payload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// An incompatible pair is rejected end to end (wasm never rides the
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

// Deprecated legacy shim: the currently-released go-toolchain autorelease
// derives upload parameters from GOOS_GOARCH filenames (name_js_wasm /
// name_wasip1_wasm), so it uploads with os=js/arch=wasm. That pair must fold
// to the canonical os=wasm form at every ingestion point -- upload, dl, and
// static canonicalization -- and "js" must never surface as an os in stored
// rows or URLs.
func TestWasmArtifact_LegacyGoosGoarchPairEndToEnd(t *testing.T) {
	t.Serial()
	env := setup(t)

	jsPayload := []byte("\x00asm\x01\x00\x00\x00legacy-js-module")
	wasip1Payload := []byte("\x00asm\x01\x00\x00\x00legacy-wasip1-module")

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"wasmlegacy","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/wasmlegacy/releases", `{"git_branch":"master","git_commit":"abc123"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Upload with the legacy GOOS/GOARCH order.
	resp = env.putBody(t, "/api/v1/projects/wasmlegacy/releases/1/artifacts/js/wasm", jsPayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	body := readBody(t, resp)
	require.Contains(t, string(body), `"os":"wasm"`)
	require.NotContains(t, string(body), `"os":"js"`, "js must never surface as an os")

	resp = env.putBody(t, "/api/v1/projects/wasmlegacy/releases/1/artifacts/wasip1/wasm", wasip1Payload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	body = readBody(t, resp)
	require.Contains(t, string(body), `"os":"wasm"`)
	require.Contains(t, string(body), `"arch":"wasip1"`)

	resp = env.postJSON(t, "/api/v1/projects/wasmlegacy/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// The stored metadata is canonical: every row is os=wasm.
	ctx := context.Background()
	proj, err := env.database.GetProject(ctx, "wasmlegacy")
	require.NoError(t, err)
	rel, err := env.database.GetRelease(ctx, proj.ID, "1")
	require.NoError(t, err)
	rows, err := env.database.ListArtifacts(ctx, rel.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, a := range rows {
		require.Equal(t, db.OSWasm, a.OS, "legacy upload must be stored under os=wasm")
	}

	// The canonical download form serves the bytes.
	resp = env.getSubdomain(t, "dl", "/wasmlegacy?v=1&os=wasm&arch=js")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	locURL, err := url.Parse(loc)
	require.NoError(t, err)
	resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, jsPayload, got)

	// The legacy pair works on dl too, and the redirect URL is canonical:
	// os=wasm, never os=js.
	for _, tc := range []struct {
		legacyOS string
		arch     string
		payload  []byte
	}{
		{"js", "js", jsPayload},
		{"wasip1", "wasip1", wasip1Payload},
	} {
		resp = env.getSubdomain(t, "dl", "/wasmlegacy?v=1&os="+tc.legacyOS+"&arch=wasm")
		require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		loc = resp.Header.Get("Location")
		resp.Body.Close()
		require.Contains(t, loc, "os=wasm")
		require.Contains(t, loc, "arch="+tc.arch)
		require.NotContains(t, loc, "os="+tc.legacyOS, "js/wasip1 must never surface as an os in URLs")

		locURL, err = url.Parse(loc)
		require.NoError(t, err)
		resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		got, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, tc.payload, got)
	}

	// Static folds the legacy pair via its canonicalization redirect, so the
	resp = env.getSubdomain(t, "static", "/file?arch=wasm&os=js&project=wasmlegacy&v=1")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	loc = resp.Header.Get("Location")
	resp.Body.Close()
	require.Contains(t, loc, "arch=js")
	require.Contains(t, loc, "os=wasm")
	require.NotContains(t, loc, "os=js")
	locURL, err = url.Parse(loc)
	require.NoError(t, err)
	resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, jsPayload, got)
}
