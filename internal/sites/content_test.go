package sites

// Serving tests for uploaded site content: files, index fallbacks, security
// headers, content types, and content length.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServe_File(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
		"style.css":  "body{}",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/style.css", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "style.css"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body{}", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "css")
}

func TestServe_SetsSiteSecurityHeaders(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html":     "<h1>hi</h1>",
		"assets/app.mjs": "export default 1;",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/assets/app.mjs", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "assets/app.mjs"})
	rec := httptest.NewRecorder()
	// The global security middleware sets these strict app headers before the
	// handler runs; serving a site must drop them so its assets can load.
	rec.Header().Set("Content-Security-Policy", "default-src 'none'")
	rec.Header().Set("X-Frame-Options", "DENY")
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Content-Security-Policy"))
	assert.Empty(t, rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	assert.Equal(t, "credentialless", rec.Header().Get("Cross-Origin-Embedder-Policy"))
}

func TestServe_IndexFallback(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: ""})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>hello</h1>", rec.Body.String())
}

func TestServe_NotFound_NoBranch(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/foo.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "foo.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServe_NotFound_NoFile(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html": "<h1>hello</h1>",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/missing.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "missing.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeRedirect(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{"index.html": "<h1>hello</h1>"})

	// A branch root requested without a trailing slash redirects to the slashed
	// form (so index.html's relative links resolve under the branch). Serve --
	// the single GET route -- handles this; there is no separate redirect route
	// that could shadow file serving.
	req := httptest.NewRequest("GET", "/sites/mysite/branch/main", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: ""})
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Security-Policy", "default-src 'none'")
	rec.Header().Set("X-Frame-Options", "DENY")
	h.Serve(rec, req)

	assert.Equal(t, http.StatusMovedPermanently, rec.Code)
	assert.Equal(t, "/sites/mysite/branch/main/", rec.Header().Get("Location"))
	assert.Empty(t, rec.Header().Get("Content-Security-Policy"))
	assert.Empty(t, rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	assert.Equal(t, "credentialless", rec.Header().Get("Cross-Origin-Embedder-Policy"))
}

func TestServe_SubdirIndex(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	uploadSite(t, h, proj, "main", map[string]string{
		"index.html":      "<h1>root</h1>",
		"docs/index.html": "<h1>docs</h1>",
	})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/docs/", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "docs/"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "<h1>docs</h1>", rec.Body.String())
}

func TestContentType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"index.html", "text/html"},
		{"style.css", "text/css"},
		{"app.js", "javascript"},
		{"font.woff2", "font/woff2"},
		{"font.woff", "font/woff"},
		{"app.mjs", "javascript"},
		{"data.bin", "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contentType(tt.name)
			assert.Contains(t, got, tt.want)
		})
	}
}

func TestServe_ContentLength(t *testing.T) {
	h, d, _ := setupTest(t)
	proj := seedProject(t, d, "mysite")
	content := "<h1>hello world</h1>"
	uploadSite(t, h, proj, "main", map[string]string{"index.html": content})

	req := httptest.NewRequest("GET", "/sites/mysite/branch/main/index.html", nil)
	req = withRoute(req, proj, route{project: "mysite", branch: "main", path: "index.html"})
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, fmt.Sprintf("%d", len(content)), rec.Header().Get("Content-Length"))
}
