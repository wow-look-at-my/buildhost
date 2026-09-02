package server_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// zipInnerName downloads the given canonical static zip URL and returns the
// archive's single entry name.
func zipInnerName(t *testing.T, env *testEnv, pathAndQuery string) string {
	t.Helper()
	resp := env.getSubdomain(t, "static", pathAndQuery)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	return zr.File[0].Name
}

func TestHashRefArtifact_UploadDownloadRoundTrip(t *testing.T) {
	env := setup(t)

	payload := []byte("#!/bin/sh\necho one-binary-many-slots\n")
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])

	// The capability is advertised on public server-info; clients gate
	resp := env.get(t, "/api/v1/server-info")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var info struct {
		UploadBySHA256 bool `json:"upload_by_sha256"`
	}
	require.NoError(t, json.Unmarshal(readBody(t, resp), &info))
	require.True(t, info.UploadBySHA256)

	resp = env.postJSON(t, "/api/v1/projects", `{"name":"hashref","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/hashref/releases", `{"git_branch":"master","git_commit":"abc123"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.putBody(t, "/api/v1/projects/hashref/releases/1/artifacts/linux/amd64", payload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// ...and the remaining slots are registered by reference. This exact slot
	// set is not a cartesian product, so the comma/alias fan-out grammar
	for _, slot := range []string{"linux/arm64", "windows/amd64"} {
		resp = env.doRequest(t, "PUT",
			"/api/v1/projects/hashref/releases/1/artifacts/"+slot+"?upload_sha256="+sumHex,
			"", nil, true)
		require.Equalf(t, http.StatusCreated, resp.StatusCode, "hash-ref %s", slot)
		require.Contains(t, string(readBody(t, resp)), sumHex)
	}

	// Naming a session keeps finalize semantics end to end: the uploads
	resp = env.doRequest(t, "PUT",
		"/api/v1/projects/hashref/releases/1/artifacts/windows/arm64?upload_session=nope&upload_sha256="+sumHex,
		"", nil, true)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/hashref/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	for _, slot := range []struct{ os, arch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"windows", "amd64"},
	} {
		// Exact-version dl resolves the slot and redirects to static.
		resp = env.getSubdomain(t, "dl", "/hashref?v=1&os="+slot.os+"&arch="+slot.arch)
		require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		require.Contains(t, loc, "static.test.local/file?")

		locURL, err := url.Parse(loc)
		require.NoError(t, err)
		resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, `attachment; filename="hashref"`, resp.Header.Get("Content-Disposition"))
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equalf(t, payload, body, "%s/%s bytes must round-trip", slot.os, slot.arch)

		// "latest" resolves per slot too.
		resp = env.getSubdomain(t, "dl", "/hashref?os="+slot.os+"&arch="+slot.arch)
		require.Equal(t, http.StatusFound, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Location"), "v=1")
		resp.Body.Close()
	}

	// Packaging derives from the ROW, not the shared blob: the windows slot's
	require.Equal(t, "hashref.exe",
		zipInnerName(t, env, "/file?arch=amd64&fmt=zip&os=windows&project=hashref&v=1"))
	require.Equal(t, "hashref",
		zipInnerName(t, env, "/file?arch=amd64&fmt=zip&os=linux&project=hashref&v=1"))
}

// A multi-slot hash-reference release on a feature branch must not hijack the
// apex "latest" pointer (the default-branch guarantee, exercised end to end
// against hash-reference rows).
func TestHashRefArtifact_FeatureBranchDoesNotMoveLatest(t *testing.T) {
	env := setup(t)

	apexPayload := []byte("apex-bytes")
	featurePayload := []byte("feature-bytes")
	fsum := sha256.Sum256(featurePayload)
	fsumHex := hex.EncodeToString(fsum[:])

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"hashreflatest","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/hashreflatest/releases", `{"git_branch":"master","git_commit":"aaa111"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.putBody(t, "/api/v1/projects/hashreflatest/releases/1/artifacts/linux/amd64", apexPayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/hashreflatest/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/hashreflatest/releases", `{"git_branch":"feature","git_commit":"bbb222"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.putBody(t, "/api/v1/projects/hashreflatest/releases/2/artifacts/linux/amd64", featurePayload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.doRequest(t, "PUT",
		"/api/v1/projects/hashreflatest/releases/2/artifacts/windows/amd64?upload_sha256="+fsumHex,
		"", nil, true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/hashreflatest/releases/2/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = env.getSubdomain(t, "dl", "/hashreflatest?os=linux&arch=amd64")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Location"), "v=1")
	resp.Body.Close()

	for _, q := range []string{"os=linux&arch=amd64", "os=windows&arch=amd64"} {
		resp = env.getSubdomain(t, "dl", "/hashreflatest?branch=feature&"+q)
		require.Equal(t, http.StatusFound, resp.StatusCode)
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		require.Contains(t, loc, "v=2")

		locURL, err := url.Parse(loc)
		require.NoError(t, err)
		resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, featurePayload, readBody(t, resp))
	}
}
