package npm

import (
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/testify/assert"
)

// HTTP-level behaviour (packuments, tarball downloads, platform packages,
// private-project auth) is tested through the real router in
// internal/server/npm_test.go. Handlers are never invoked directly with a
// hand-built request context, so route registration, path parsing and the
// auth middleware are all exercised end to end. The tests here cover only
// the pure helper functions.

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
