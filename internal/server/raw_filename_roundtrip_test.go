package server_test

// End-to-end proof that the raw download is served under the name the artifact
// was PUBLISHED with (the stored artifact filename), not the project name.
//
// A publish sub-project like "log-streamer/client" stages its binary under a
// project name that differs from the binary's own name; the project is created
// here as "mylog" (the plain test env, unlike the OIDC-namespaced publish flow,
// cannot create slash-namespaced sub-projects). The binary is uploaded with
// X-Artifact-Filename: log-streamer-client, so the stored artifact row records
// filename "log-streamer-client", and the raw (/file?...fmt=raw) download MUST
// serve Content-Disposition: attachment; filename="log-streamer-client", NOT
// "mylog". Uploads that omit X-Artifact-Filename keep the previous project-name
// behavior byte-for-byte.

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRawFilename_StoredUploadFilenameIsServed drives the real
// upload -> publish -> dl -> static/raw path for an artifact uploaded WITH an
// X-Artifact-Filename header, and asserts the raw response serves that stored
// filename rather than the project name.
func TestRawFilename_StoredUploadFilenameIsServed(t *testing.T) {
	t.Serial()
	env := setup(t)

	payload := []byte("#!/bin/sh\necho served-under-stored-filename\n")

	// The project is named independently of the binary, exactly as a publish
	// sub-project ("log-streamer/client") is -- the artifact's on-disk identity
	// is its STORED filename, which is what the raw download must serve.
	require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects",
		`{"name":"mylog","versioning":"auto"}`).StatusCode)
	env.postJSON(t, "/api/v1/projects/mylog/releases",
		`{"git_branch":"master","git_commit":"abc123"}`).Body.Close()

	// Upload with the filename the binary is published as. doFullHost lets us
	// set the X-Artifact-Filename header that the real ingester sanitizes into
	// artifact.filename.
	u, _ := url.Parse(env.ts.URL)
	resp := env.doFullHost(t, "PUT", u.Host,
		"/api/v1/projects/mylog/releases/1/artifacts/linux/amd64",
		"application/octet-stream",
		map[string]string{"X-Artifact-Filename": "log-streamer-client"},
		bytes.NewReader(payload), true)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/mylog/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// dl resolves the slot and redirects to static.
	resp = env.getSubdomain(t, "dl", "/mylog?v=1&os=linux&arch=amd64")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	loc := resp.Header.Get("Location")
	resp.Body.Close()
	require.Contains(t, loc, "static.test.local/file?")

	// static serves the raw bytes under the STORED filename, not the project name.
	locURL, err := url.Parse(loc)
	require.NoError(t, err)
	resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, `attachment; filename="log-streamer-client"`,
		resp.Header.Get("Content-Disposition"))
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, payload, body)
}

// TestRawFilename_NoUploadFilenameFallsBackToProjectName proves the no-header
// case keeps serving the project name exactly as before (criterion 2: the
// single-segment "hashref" round-trip and other per-platform uploads are
// unchanged). It is the mirror of the stored-filename case.
func TestRawFilename_NoUploadFilenameFallsBackToProjectName(t *testing.T) {
	t.Serial()
	env := setup(t)

	payload := []byte("#!/bin/sh\necho served-under-project-name\n")

	require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects",
		`{"name":"plainbin","versioning":"auto"}`).StatusCode)
	env.postJSON(t, "/api/v1/projects/plainbin/releases",
		`{"git_branch":"master","git_commit":"abc123"}`).Body.Close()

	// No X-Artifact-Filename header: a plain per-platform upload.
	resp := env.putBody(t, "/api/v1/projects/plainbin/releases/1/artifacts/linux/amd64", payload)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = env.postJSON(t, "/api/v1/projects/plainbin/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = env.getSubdomain(t, "dl", "/plainbin?v=1&os=linux&arch=amd64")
	require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
	loc := resp.Header.Get("Location")
	resp.Body.Close()

	locURL, err := url.Parse(loc)
	require.NoError(t, err)
	resp = env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, `attachment; filename="plainbin"`, resp.Header.Get("Content-Disposition"))
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, payload, body)
}