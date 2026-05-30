package llms

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestServe_RendersBaseURL(t *testing.T) {
	h := &Handler{body: render("https://pazer.build")}

	req := httptest.NewRequest("GET", "/llms.txt", nil)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age")

	body := rec.Body.String()
	assert.Contains(t, body, "# buildhost")
	assert.Contains(t, body, "https://pazer.build/dl/myapp")
	assert.Contains(t, body, "https://pazer.build/llms.txt")
	assert.Contains(t, body, "docker pull pazer.build/myapp:latest")
	assert.NotContains(t, body, "__BASE_URL__")
	assert.NotContains(t, body, "__DL_URL__")
	assert.NotContains(t, body, "__OCI_HOST__")
}

func TestServe_DefaultsWhenNotReady(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest("GET", "/llms.txt", nil)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "# buildhost")
	assert.Contains(t, rec.Body.String(), "http://localhost:8080/llms.txt")
}

func TestRender_TrimsTrailingSlash(t *testing.T) {
	body := string(render("https://pazer.build/"))
	assert.Contains(t, body, "https://pazer.build/llms.txt")
	assert.NotContains(t, body, "https://pazer.build//llms.txt")
}

func TestRender_StripsSchemeForHost(t *testing.T) {
	assert.Contains(t, string(render("http://localhost:8080")), "docker pull localhost:8080/myapp")
	assert.Contains(t, string(render("https://example.com")), "docker pull example.com/myapp")
}

func TestTemplate_NoUnrenderedPlaceholdersAndASCII(t *testing.T) {
	for _, ph := range []string{"__BASE_URL__", "__DL_URL__", "__OCI_HOST__"} {
		assert.True(t, contains(templateMD, ph), "template should use %s", ph)
	}
	for i := 0; i < len(templateMD); i++ {
		c := templateMD[i]
		assert.Truef(t, c == '\n' || c == '\t' || (c >= 0x20 && c <= 0x7e),
			"non-ASCII byte 0x%02x at offset %d", c, i)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
