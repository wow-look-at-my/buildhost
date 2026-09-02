package goproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMirror stands in for an operator-configured mirror: it serves the
// module-proxy protocol for a fixed set of paths and 404s everything else.
func fakeMirror(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpstreamModuleIsServedAndCached(t *testing.T) {
	mirror := fakeMirror(t, map[string]string{
		"golang.org/x/mod/@v/list":         "v0.39.0\nv0.40.0\n",
		"golang.org/x/mod/@latest":         `{"Version":"v0.40.0","Time":"2026-01-02T03:04:05Z"}`,
		"golang.org/x/mod/@v/v0.40.0.info": `{"Version":"v0.40.0","Time":"2026-01-02T03:04:05Z"}`,
		"golang.org/x/mod/@v/v0.40.0.mod":  "module golang.org/x/mod\n",
		"golang.org/x/mod/@v/v0.40.0.zip":  "PK\x03\x04 pretend zip",
	})

	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.upstream = newUpstreamSource(s.github.client, mirror.URL, []string{privateOrg})

	t.Run("list", func(t *testing.T) {
		rec := serveProxy(t, s, "/golang.org/x/mod/@v/list")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "v0.39.0\nv0.40.0\n", rec.Body.String())
	})

	t.Run("latest", func(t *testing.T) {
		rec := serveProxy(t, s, "/golang.org/x/mod/@latest")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "v0.40.0")
	})

	t.Run("mod", func(t *testing.T) {
		rec := serveProxy(t, s, "/golang.org/x/mod/@v/v0.40.0.mod")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "module golang.org/x/mod\n", rec.Body.String())
	})

	t.Run("zip is cached as a blob", func(t *testing.T) {
		rec := serveProxy(t, s, "/golang.org/x/mod/@v/v0.40.0.zip")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "PK\x03\x04 pretend zip", rec.Body.String())
		assert.Equal(t, "application/zip", rec.Header().Get("Content-Type"))

		cached, err := s.db.GetGoproxyCached(t.Context(), "golang.org/x/mod", "v0.40.0")
		require.NoError(t, err)
		assert.NotEmpty(t, cached.ZipKey, "an upstream zip must be stored, not just proxied")
		assert.Positive(t, cached.ZipSize)
	})

	t.Run("cached module is recorded as upstream", func(t *testing.T) {
		mods, err := s.db.ListGoproxyModules(t.Context())
		require.NoError(t, err)
		require.Len(t, mods, 1)
		assert.Equal(t, "golang.org/x/mod", mods[0].ModulePath)
		assert.Equal(t, "upstream", mods[0].Source)
		assert.Empty(t, mods[0].LastErrorKind)
	})
}

func TestUpstreamMissIs404(t *testing.T) {
	mirror := fakeMirror(t, nil)
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.upstream = newUpstreamSource(s.github.client, mirror.URL, []string{privateOrg})

	rec := serveProxy(t, s, "/example.com/nope/@v/list")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "module not found")
}

// The default shape: no mirror configured, so a module outside this proxy's
func TestModuleOutsideOurNamespaceIs404SoDirectCanTakeIt(t *testing.T) {
	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/golang.org/x/mod/@v/list")

	require.Equal(t, http.StatusNotFound, rec.Code,
		"only 404/410 make the go command advance to the next GOPROXY entry")
	body := rec.Body.String()
	assert.Contains(t, body, "not served by this proxy")
	assert.Contains(t, body, privateOrg, "the body should say what this proxy does serve")
	assert.Contains(t, body, ",direct")
}

func TestAuthorizationFailureDoesNotFallThroughToDirect(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.Private = true
	s := newTestService(t, fake, "", []string{privateOrg})

	rec := serveProxy(t, s, "/"+privateOrg+"/tml/@v/list")

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotEqual(t, http.StatusNotFound, rec.Code,
		"a 404 here would let the go command silently try direct and hide the credential failure")
}

// A mirror that is up but broken is an upstream failure, not an absence.
func TestUpstreamServerErrorIsNotAMissingModule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "mirror exploded", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	fake := newFakeGitHub(t)
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.upstream = newUpstreamSource(s.github.client, srv.URL, []string{privateOrg})

	rec := serveProxy(t, s, "/golang.org/x/mod/@v/list")
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "mirror exploded")
}

// A private-prefix module must never be sent to the public mirror: that would
func TestPrivateModuleNeverReachesTheMirror(t *testing.T) {
	var mirrorHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mirrorHits++
		http.Error(w, "should not be asked", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	fake := newFakeGitHub(t)
	seedModule(fake, privateOrg+"/tml", "", "v1.0.0", "aaaa111122223333444455556666777788889999",
		"module "+privateOrg+"/tml\n")
	s := newTestService(t, fake, "tok", []string{privateOrg})
	s.upstream = newUpstreamSource(s.github.client, srv.URL, []string{privateOrg})

	require.Equal(t, http.StatusOK, serveProxy(t, s, "/"+privateOrg+"/tml/@v/list").Code)
	assert.Zero(t, mirrorHits, "a private module path must not be sent to the public mirror")
}
