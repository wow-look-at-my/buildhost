package server_test

// End-to-end proofs that the raw download is served under the name the artifact
// was PUBLISHED with (the stored artifact filename) when one was uploaded, and
// otherwise falls back to the project name byte-for-byte.
//
// A publish sub-project like "log-streamer/client" stages its binary under a
// project name that differs from the binary's own name; the project is created
// here as "mylog" (the plain test env, unlike the OIDC-namespaced publish flow,
// cannot create slash-namespaced sub-projects). The has-filename case uploads
// with X-Artifact-Filename: log-streamer-client, so the stored artifact row
// records "log-streamer-client" and the raw (/file?...fmt=raw) download MUST
// serve Content-Disposition: attachment; filename="log-streamer-client", NOT
// "mylog". The no-filename case asserts the previous project-name behavior.

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRawFilename_ServesStoredOrFallsBackToProject drives the real
// upload -> publish -> dl -> static/raw path for both scenarios and asserts the
// served Content-Disposition:
//   - with an X-Artifact-Filename header the STORED filename wins over the
//     project name (log-streamer-client vs mylog);
//   - without the header the project name is served, so per-platform uploads
//     (the hashref round-trip and every existing single-segment project) keep
//     today's behavior byte-for-byte.
func TestRawFilename_ServesStoredOrFallsBackToProject(t *testing.T) {
	for _, tc := range []struct {
		name       string
		project    string
		uploadFile string // X-Artifact-Filename header value; "" means omit
		wantCD     string
	}{
		{"stores-uploaded-filename", "mylog", "log-streamer-client", `attachment; filename="log-streamer-client"`},
		{"falls-back-to-project-name", "plainbin", "", `attachment; filename="plainbin"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Serial()
			env := setup(t)

			payload := []byte("#!/bin/sh\necho raw-filename-case\n")

			require.Equal(t, http.StatusCreated, env.postJSON(t, "/api/v1/projects",
				`{"name":"`+tc.project+`","versioning":"auto"}`).StatusCode)
			env.postJSON(t, "/api/v1/projects/"+tc.project+"/releases",
				`{"git_branch":"master","git_commit":"abc123"}`).Body.Close()

			// Upload, optionally declaring the binary's published name.
			var resp *http.Response
			if tc.uploadFile == "" {
				resp = env.putBody(t, "/api/v1/projects/"+tc.project+"/releases/1/artifacts/linux/amd64", payload)
			} else {
				u, _ := url.Parse(env.ts.URL)
				resp = env.doFullHost(t, "PUT", u.Host,
					"/api/v1/projects/"+tc.project+"/releases/1/artifacts/linux/amd64",
					"application/octet-stream",
					map[string]string{"X-Artifact-Filename": tc.uploadFile},
					bytes.NewReader(payload), true)
			}
			require.Equal(t, http.StatusCreated, resp.StatusCode)
			resp.Body.Close()

			resp = env.postJSON(t, "/api/v1/projects/"+tc.project+"/releases/1/publish", `{}`)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			// dl resolves the slot and redirects to static.
			resp = env.getSubdomain(t, "dl", "/"+tc.project+"?v=1&os=linux&arch=amd64")
			require.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			require.Contains(t, loc, "static.test.local/file?")

			// static serves the raw bytes under the asserted name.
			locURL, err := url.Parse(loc)
			require.NoError(t, err)
			raw := env.getSubdomain(t, "static", locURL.Path+"?"+locURL.RawQuery)
			require.Equal(t, http.StatusOK, raw.StatusCode)
			require.Equal(t, tc.wantCD, raw.Header.Get("Content-Disposition"))
			body, err := io.ReadAll(raw.Body)
			raw.Body.Close()
			require.NoError(t, err)
			require.Equal(t, payload, body)
		})
	}
}