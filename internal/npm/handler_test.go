package npm

import (
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/testify/assert"
)

// HTTP-level behaviour (packuments, tarball downloads, platform packages,
// private-project auth) is tested through the real router in router_test.go.
// Handlers are never invoked directly with a hand-built request context, so
// route registration, path parsing and the auth middleware are all exercised
// end to end. The tests here cover the pure parsing/helper functions.

func TestParseRoute(t *testing.T) {
	tests := []struct {
		name     string
		pathVal  string
		wantProj string
		wantPlat string
	}{
		{"simple", "myapp", "myapp", ""},
		{"numeric", "app123", "app123", ""},
		{"dotted", "my.app", "my.app", ""},
		{"hyphenated", "go-toolchain", "go-toolchain", ""},
		{"multi-hyphen", "my-cool-app", "my-cool-app", ""},
		{"many-hyphens", "a-b-c-d-e", "a-b-c-d-e", ""},
		{"namespaced", "cc-marketplace__my-plugin", "cc-marketplace/my-plugin", ""},
		{"platform linux-x64", "go-toolchain-linux-x64", "go-toolchain", "linux-x64"},
		{"platform darwin-arm64", "go-toolchain-darwin-arm64", "go-toolchain", "darwin-arm64"},
		{"platform win32-x64", "myapp-win32-x64", "myapp", "win32-x64"},
		{"platform linux-arm64", "myapp-linux-arm64", "myapp", "linux-arm64"},
		{"platform linux-ia32", "myapp-linux-ia32", "myapp", "linux-ia32"},
		{"platform darwin-x64", "myapp-darwin-x64", "myapp", "darwin-x64"},
		{"platform win32-arm64", "myapp-win32-arm64", "myapp", "win32-arm64"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/@buildhost/"+tt.pathVal, nil)
			// The npm route captures the whole scoped segment; the router has
			// already percent-decoded it to `@buildhost/<name>`.
			req.SetPathValue("pkg", "@buildhost/"+tt.pathVal)
			ri := parseRoute(req).(route)
			assert.Equal(t, tt.wantProj, ri.project, "project")
			assert.Equal(t, tt.wantPlat, ri.platform, "platform")
		})
	}

	// A path without the @buildhost scope is not a package request.
	req := httptest.NewRequest("GET", "/favicon.ico", nil)
	req.SetPathValue("pkg", "favicon.ico")
	assert.Equal(t, "", parseRoute(req).(route).project)
}

func TestSplitPlatform(t *testing.T) {
	tests := []struct {
		input    string
		wantProj string
		wantPlat string
	}{
		{"myapp", "myapp", ""},
		{"go-toolchain", "go-toolchain", ""},
		{"go-toolchain-linux-x64", "go-toolchain", "linux-x64"},
		{"go-toolchain-darwin-arm64", "go-toolchain", "darwin-arm64"},
		{"go-toolchain-win32-x64", "go-toolchain", "win32-x64"},
		{"my-cool-app-linux-arm64", "my-cool-app", "linux-arm64"},
		{"app-linux-ia32", "app", "linux-ia32"},
		{"a-b-c-darwin-x64", "a-b-c", "darwin-x64"},
		// Not a known platform - treated as project name
		{"myapp-freebsd-amd64", "myapp-freebsd-amd64", ""},
		{"myapp-linux", "myapp-linux", ""},
		{"myapp-x64", "myapp-x64", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			proj, plat := splitPlatform(tt.input)
			assert.Equal(t, tt.wantProj, proj, "project")
			assert.Equal(t, tt.wantPlat, plat, "platform")
		})
	}
}

func TestWrapperRunScript(t *testing.T) {
	script := wrapperRunScript("mytool")
	assert.NotEmpty(t, script)
	assert.Contains(t, script, "mytool")
	assert.Contains(t, script, "@buildhost/mytool")

	assert.Empty(t, wrapperRunScript("bad name!"))
	assert.Empty(t, wrapperRunScript("UPPERCASE"))
}

func TestProjectNPMNameRoundTrip(t *testing.T) {
	for _, tt := range []struct{ project, npm string }{
		{"go-toolchain", "go-toolchain"},
		{"cc-marketplace/my-plugin", "cc-marketplace__my-plugin"},
		{"a/b/c", "a__b__c"},
	} {
		assert.Equal(t, tt.npm, projectToNPMName(tt.project), "encode %q", tt.project)
		assert.Equal(t, tt.project, npmNameToProject(tt.npm), "decode %q", tt.npm)
	}
}

func TestPlatformHelpers(t *testing.T) {
	assert.Equal(t, "darwin", npmPlatform(db.OSDarwin))
	assert.Equal(t, "win32", npmPlatform(db.OSWindows))
	assert.Equal(t, "linux", npmPlatform(db.OSLinux))

	assert.Equal(t, "x64", npmArch(db.ArchAMD64))
	assert.Equal(t, "arm64", npmArch(db.ArchARM64))
	assert.Equal(t, "ia32", npmArch(db.Arch386))
	assert.Equal(t, "arm", npmArch(db.Arch("arm")))

	assert.Equal(t, "windows", reverseNpmPlatform("win32"))
	assert.Equal(t, "darwin", reverseNpmPlatform("darwin"))

	assert.Equal(t, "amd64", reverseNpmArch("x64"))
	assert.Equal(t, "386", reverseNpmArch("ia32"))
	assert.Equal(t, "arm64", reverseNpmArch("arm64"))
}

func TestNormalizeVersion(t *testing.T) {
	assert.Equal(t, "1.2.3", normalizeVersion("1.2.3"))
	assert.Equal(t, "1.2.3", normalizeVersion("v1.2.3"))
	assert.Equal(t, "1.0.0", normalizeVersion("1"))
	assert.Equal(t, "2.0.0", normalizeVersion("v2"))
}

// TestServeHTTP_RoutedRealNpmRequest drives requests through the real subdomain
// dispatch and router exactly as `npm install` does. npm percent-encodes the
// scope slash, so the packument path is `/@buildhost%2f<name>`. The other
// ServeHTTP tests inject the parsed route directly and so never exercise
// routing -- which is why the %2f mismatch went unnoticed until production.
