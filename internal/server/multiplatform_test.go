package server_test

// End-to-end proof of single-artifact multi-platform ingest: ONE uploaded APE
// becomes ONE artifact, and every platform it covers downloads the identical
// bytes from the identical URL.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apeBytes builds a payload that passes the APE magic check.
func apeBytes(t *testing.T) []byte {
	t.Helper()
	body := make([]byte, 256)
	_, err := rand.Read(body)
	require.NoError(t, err)
	return append([]byte("MZqFpD"), body...)
}

// seedAPERelease creates a project + release and PUTs one APE covering the
// three platforms gosmopolitan's fat build targets. It returns the payload and
// the artifact the server recorded.
func seedAPERelease(t *testing.T, env *testEnv, project string) ([]byte, db.ArtifactWithPlatforms) {
	t.Helper()
	payload := apeBytes(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"`+project+`","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/"+project+"/releases", `{"git_branch":"master"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	uploadPath := "/api/v1/projects/" + project + "/releases/1/artifacts/ape" +
		"?platforms=linux/amd64,darwin/arm64,windows/amd64"
	req := mustRequest(t, "PUT", env.ts.URL+uploadPath, payload)
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("X-Artifact-Filename", project)
	resp = mustDo(t, req)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var artifact db.ArtifactWithPlatforms
	decodeJSON(t, resp, &artifact)
	return payload, artifact
}

func mustRequest(t *testing.T, method, url string, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/octet-stream")
	return req
}

func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// TestMultiPlatformAPE_OneUploadOneRowOneURL is the deliverable's end-to-end
// contract: upload once, ask for three different platforms, get one artifact,
// one static URL, one digest and one ETag every time.
func TestMultiPlatformAPE_OneUploadOneRowOneURL(t *testing.T) {
	env := setup(t)
	payload, artifact := seedAPERelease(t, env, "apehost")
	wantSHA := sha256.Sum256(payload)

	assert.Equal(t, hex.EncodeToString(wantSHA[:]), artifact.SHA256)
	assert.Equal(t, "ape", artifact.ExeFormat)
	require.Len(t, artifact.Platforms, 3)

	// One artifact row, not three.
	resp := env.authGet(t, "/api/v1/projects/apehost/releases/1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var release struct {
		Artifacts []db.ArtifactWithPlatforms `json:"artifacts"`
	}
	decodeJSON(t, resp, &release)
	require.Len(t, release.Artifacts, 1, "one file must be one artifact")
	assert.Equal(t, artifact.Platforms, release.Artifacts[0].Platforms)

	resp = env.postJSON(t, "/api/v1/projects/apehost/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Every covered platform: same redirect target, same bytes, same ETag.
	var locations, etags []string
	for _, spelling := range []struct{ os, arch string }{
		{"linux", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
		// Alias spellings a CI runner passes through verbatim.
		{"macOS", "aarch64"},
		{"Windows", "X64"},
	} {
		q := url.Values{"os": {spelling.os}, "arch": {spelling.arch}}
		resp = env.doSubdomainRequest(t, "GET", "dl", "/apehost?"+q.Encode(), "", nil, true)
		require.Equal(t, http.StatusFound, resp.StatusCode, "%s/%s", spelling.os, spelling.arch)
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		require.NotEmpty(t, loc)
		locations = append(locations, loc)

		staticURL, err := url.Parse(loc)
		require.NoError(t, err)
		resp = env.doSubdomainRequest(t, "GET", "static", staticURL.RequestURI(), "", nil, true)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s/%s", spelling.os, spelling.arch)
		got := readBody(t, resp)
		gotSHA := sha256.Sum256(got)
		assert.Equal(t, wantSHA, gotSHA, "%s/%s must serve the identical file", spelling.os, spelling.arch)
		etags = append(etags, resp.Header.Get("ETag"))
	}

	for i := range locations {
		assert.Equal(t, locations[0], locations[i], "every platform must redirect to ONE URL")
		assert.Equal(t, etags[0], etags[i], "every platform must share ONE ETag")
		assert.NotEmpty(t, etags[i])
	}
}

// TestMultiPlatformAPE_ReleasePageShowsOneLinkWithBadge proves the public,
// no-JavaScript frontend renders the owner-visible outcome: one download link
// row carrying an "APE: <platforms>" badge.
func TestMultiPlatformAPE_ReleasePageShowsOneLinkWithBadge(t *testing.T) {
	env := setup(t)
	seedAPERelease(t, env, "apeweb")

	resp := env.postJSON(t, "/api/v1/projects/apeweb/releases/1/publish", `{}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = env.authGet(t, "/projects/apeweb/releases/1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	page := string(body)

	assert.Contains(t, page, `class="badge badge-format"`)
	assert.Contains(t, page, "APE: ")
	assert.Contains(t, page, "linux/amd64, darwin/arm64, windows/amd64")
	// Exactly one raw download link: one file, one link.
	assert.Equal(t, 1, strings.Count(page, `>raw</a>`))
}

// A non-APE upload cannot claim several platforms, and the rejection stores
// nothing.
func TestMultiPlatformAPE_NonAPERejectedEndToEnd(t *testing.T) {
	env := setup(t)

	resp := env.postJSON(t, "/api/v1/projects", `{"name":"notape","versioning":"auto"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	resp = env.postJSON(t, "/api/v1/projects/notape/releases", `{"git_branch":"master"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	req := mustRequest(t, "PUT",
		env.ts.URL+"/api/v1/projects/notape/releases/1/artifacts/ape?platforms=linux/amd64,darwin/arm64",
		[]byte("\x7fELF-ordinary-binary"))
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp = mustDo(t, req)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	resp = env.authGet(t, "/api/v1/projects/notape/releases/1")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var release struct {
		Artifacts []db.ArtifactWithPlatforms `json:"artifacts"`
	}
	decodeJSON(t, resp, &release)
	assert.Empty(t, release.Artifacts)
}
