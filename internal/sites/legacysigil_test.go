package sites

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// "~" is the SUBDOMAIN scheme's original branch sigil, and only that scheme's.
// On the classic sites.{domain}/{project}/... scheme it has never been a sigil:
// it is an ordinary path character, so /{project}/~{branch}/{file} addresses a
// file literally named "~{branch}/{file}" under the project's default branch.
//
// This is pinned because the top-level CLAUDE.md claimed the opposite -- that
// "the older /branch/{branch}/ and ~ spellings still serve the same files, as
// 302s to the canonical URL" -- which describes the classic scheme and is true
// only of /branch/. Believing it costs real time: a live probe of
func TestLegacySigil_ClassicSchemeTreatsTildeAsAnOrdinaryPathSegment(t *testing.T) {
	t.Serial()
	env := setupEnv(t)
	seedProject(t, env.db, "lib")
	// master is the seed default branch, so it serves at the bare project path.
	env.uploadSite(t, "lib", "master", map[string]string{
		"index.html":         "<h1>default</h1>",
		"~library/ui/mod.js": "// a real file that merely looks like a sigil",
		"ui/mod.js":          "// default-branch module",
	})
	env.uploadSite(t, "lib", "library", map[string]string{"ui/mod.js": "// LIBRARY BRANCH module"})

	// The decisive assertion: "~library/..." resolves as a PATH, serving the
	rec := env.do(t, "GET", "/lib/~library/ui/mod.js", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "// a real file that merely looks like a sigil", rec.Body.String(),
		"~library must address a literal path on the classic scheme, never the library branch")

	rec = env.do(t, "GET", "/lib/~library/ui/missing.js", "", nil, false)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Emptyf(t, rec.Header().Get("Location"), "~ must not redirect on the classic scheme")

	// The sigil that DOES work on this scheme, for contrast.
	rec = env.do(t, "GET", "/lib/@library/ui/mod.js", "", nil, false)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "// LIBRARY BRANCH module", rec.Body.String())
}

// The other half of the asymmetry: on {project}.<site-domain>, "~" IS a sigil
// and 301s to the "@" spelling, which is what "no published URL breaks" means
// and where the legacy sigil actually earns its name.
func TestLegacySigil_SubdomainSchemeRedirectsToAt(t *testing.T) {
	t.Serial()
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "lib")
	uploadSite(t, h, proj, "pr-7", map[string]string{"index.html": "preview"})

	req := httptest.NewRequest("GET", "http://lib.sites.example.com/~pr-7/index.html", nil)
	req = withRoute(req, proj, route{project: "lib", sigil: "pr-7/index.html"})
	rec := httptest.NewRecorder()
	h.ServeSubdomain(rec, req)

	require.Equal(t, http.StatusMovedPermanently, rec.Code, rec.Body.String())
	assert.Equal(t, "/@pr-7/index.html", rec.Header().Get("Location"))
}
