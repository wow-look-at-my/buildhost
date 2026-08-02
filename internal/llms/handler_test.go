package llms

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The publish composites download the CLI rather than building it, so the
// guide has to name the URL they depend on.
func TestServe_DocumentsCLIDownload(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest("GET", "https://pazer.build/llms.txt", nil)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Contains(t, rec.Body.String(), "https://dl.pazer.build/buildhost?os=linux&arch=amd64")
}

func TestServe_RendersBaseURL(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest("GET", "https://pazer.build/llms.txt", nil)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age")

	body := rec.Body.String()
	assert.Contains(t, body, "# buildhost")
	assert.Contains(t, body, "https://dl.pazer.build/myapp")
	assert.Contains(t, body, "https://pazer.build/llms.txt")
	assert.Contains(t, body, "brew tap pazer/build https://brew.pazer.build/tap.git")
	assert.Contains(t, body, "brew trust pazer/build")
	assert.Contains(t, body, "brew install pazer/build/go-toolchain")
	// The authenticated-tap URL macro renders scheme + creds placeholder +
	// brew host; "$TOKEN" stays a literal shell placeholder for the reader.
	assert.Contains(t, body, `brew tap pazer/build "https://x:$TOKEN@brew.pazer.build/private/tap.git"`)
	assert.Contains(t, body, "brew install pazer/build/myrepo-myapp")
	assert.NotContains(t, body, "__BREW_TOKEN_URL__")
	assert.NotContains(t, body, "brew install https://brew.pazer.build/myapp")
	assert.Contains(t, body, "docker pull oci.pazer.build/myapp:latest")
	assert.NotContains(t, body, "__BASE_URL__")
	assert.NotContains(t, body, "__DL_URL__")
	assert.NotContains(t, body, "__OCI_HOST__")
}

func TestServe_RendersRequestHost(t *testing.T) {
	h := &Handler{}

	req := httptest.NewRequest("GET", "https://builds.example.com/llms.txt", nil)
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "# buildhost")
	assert.Contains(t, rec.Body.String(), "https://builds.example.com/llms.txt")
}

func TestApexBaseURL_StripsServiceSubdomain(t *testing.T) {
	// /llms.txt is served on the apex and every service subdomain, but the
	// rendered service URLs must always anchor to the apex. apexBaseURL strips a
	// known leading service label so a request on oci.<apex> renders dl.<apex>
	// rather than dl.oci.<apex>.
	cases := []struct {
		host string
		want string
	}{
		{"pazer.build", "https://pazer.build"},               // apex: unchanged
		{"oci.pazer.build", "https://pazer.build"},           // service label stripped
		{"npm.pazer.build", "https://pazer.build"},           // ditto
		{"static.pazer.build", "https://pazer.build"},        // ditto
		{"builds.example.com", "https://builds.example.com"}, // non-service first label kept
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},          // bare IP, loopback scheme, port kept
		{"oci.localhost:8080", "http://localhost:8080"},      // strip + .localhost scheme + port
		{"localhost:9000", "http://localhost:9000"},          // single-label host: nothing to strip
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/llms.txt", nil)
			req.Host = tc.host
			assert.Equal(t, tc.want, apexBaseURL(req))
		})
	}
}

func TestServe_OnServiceSubdomain_AnchorsURLsToApex(t *testing.T) {
	h := &Handler{}

	// A request arriving on a service subdomain still renders apex-anchored URLs.
	req := httptest.NewRequest("GET", "/llms.txt", nil)
	req.Host = "oci.pazer.build"
	rec := httptest.NewRecorder()
	h.Serve(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "# buildhost")
	assert.Contains(t, body, "https://dl.pazer.build/myapp")
	assert.Contains(t, body, "docker pull oci.pazer.build/myapp:latest")
	// No double-prefixed host such as dl.oci.pazer.build / oci.oci.pazer.build.
	assert.NotContains(t, body, ".oci.pazer.build")
}

func TestRender_TrimsTrailingSlash(t *testing.T) {
	body := string(render("https://pazer.build/"))
	assert.Contains(t, body, "https://pazer.build/llms.txt")
	assert.NotContains(t, body, "https://pazer.build//llms.txt")
}

func TestRender_OCIHostSubdomain(t *testing.T) {
	// __OCI_HOST__ is the oci.<host> registry subdomain (scheme stripped).
	assert.Contains(t, string(render("http://localhost:8080")), "docker pull oci.localhost:8080/myapp")
	assert.Contains(t, string(render("https://example.com")), "docker pull oci.example.com/myapp")
}

func TestRender_ServiceSubdomains(t *testing.T) {
	// Service URLs are subdomains of the base host, not paths.
	body := string(render("https://pazer.build"))
	assert.Contains(t, body, "https://dl.pazer.build/myapp")
	assert.Contains(t, body, "https://sites.pazer.build/myapp/@main/")
	// The older spelling is documented as still working, so it must stay in the guide.
	assert.Contains(t, body, "/myapp/branch/main/x.css")
	assert.NotContains(t, body, "https://pazer.build/dl/")
	assert.NotContains(t, body, "https://pazer.build/sites/")
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
