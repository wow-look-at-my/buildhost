package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidOS(t *testing.T) {
	valid := []string{"linux", "darwin", "windows", "freebsd", "wasm"}
	for _, s := range valid {
		assert.True(t, ValidOS(s))
	}

	invalid := []string{"", "Linux", "LINUX", "android", "ios", "plan9", "js", "wasip1"}
	for _, s := range invalid {
		assert.False(t, ValidOS(s))
	}
}

func TestValidArch(t *testing.T) {
	valid := []string{"amd64", "arm64", "386", "arm", "js", "wasip1"}
	for _, s := range valid {
		assert.True(t, ValidArch(s))
	}

	invalid := []string{"", "x86_64", "aarch64", "mips", "AMD64", "wasm", "wasi"}
	for _, s := range invalid {
		assert.False(t, ValidArch(s))
	}
}

func TestCompatiblePlatform(t *testing.T) {
	compatible := [][2]string{
		{"wasm", "js"}, {"wasm", "wasip1"},
		{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "amd64"},
		{"freebsd", "386"},
		// Coherence check only: it does not re-validate individual values.
		{"any", "any"},
	}
	for _, c := range compatible {
		assert.True(t, CompatiblePlatform(OS(c[0]), Arch(c[1])), "%s/%s should be compatible", c[0], c[1])
	}

	incompatible := [][2]string{
		{"wasm", "amd64"}, {"wasm", "arm64"}, {"wasm", "any"},
		{"linux", "js"}, {"darwin", "wasip1"}, {"windows", "js"},
	}
	for _, c := range incompatible {
		assert.False(t, CompatiblePlatform(OS(c[0]), Arch(c[1])), "%s/%s should be incompatible", c[0], c[1])
	}
}

func TestValidKind(t *testing.T) {
	valid := []string{"binary", "library", "assets", "archive"}
	for _, s := range valid {
		assert.True(t, ValidKind(s))
	}

	invalid := []string{"", "Binary", "source", "container", "image"}
	for _, s := range invalid {
		assert.False(t, ValidKind(s))
	}
}

func TestAPITokenHasScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		scope  string
		want   bool
	}{
		{"single scope match", "read", "read", true},
		{"single scope no match", "read", "write", false},
		{"multiple scopes first", "read,write,admin", "read", true},
		{"multiple scopes middle", "read,write,admin", "write", true},
		{"multiple scopes last", "read,write,admin", "admin", true},
		{"multiple scopes no match", "read,write", "admin", false},
		{"empty scopes", "", "read", false},
		{"empty scope query", "read,write", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := APIToken{Scopes: tt.scopes}
			got := tok.HasScope(tt.scope)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReleaseIsPrerelease(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v1.0.0", false},
		{"v1.0.0-beta.1", true},
		{"v2.3.4-rc1", true},
		{"v0.1.0-alpha", true},
		{"1.0.0", false},
		{"1.0.0-dev", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			r := Release{Version: tt.version}
			got := r.IsPrerelease()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeOS(t *testing.T) {
	cases := map[string]OS{
		"linux": OSLinux, "Linux": OSLinux,
		"darwin": OSDarwin, "macOS": OSDarwin, "macos": OSDarwin, "osx": OSDarwin, "  MAC  ": OSDarwin,
		"windows": OSWindows, "Windows": OSWindows, "win": OSWindows,
		"freebsd": OSFreeBSD,
		"wasm":    OSWasm, "WASM": OSWasm, "WebAssembly": OSWasm,
	}
	for in, want := range cases {
		got, ok := NormalizeOS(in)
		assert.True(t, ok, "expected %q to normalize", in)
		assert.Equal(t, want, got, "for input %q", in)
	}

	// "js" is Go's GOOS spelling of the browser wasm port, but the buildhost
	// platform identifier is os=wasm with the arch naming the flavor -- "js"
	// and "wasip1" must not normalize as an OS.
	for _, in := range []string{"", "any", "android", "plan9", "js", "wasip1"} {
		_, ok := NormalizeOS(in)
		assert.False(t, ok, "expected %q to be unrecognized", in)
	}
}

func TestNormalizeArch(t *testing.T) {
	cases := map[string]Arch{
		"amd64": ArchAMD64, "X64": ArchAMD64, "x86_64": ArchAMD64, "x86-64": ArchAMD64,
		"arm64": ArchARM64, "ARM64": ArchARM64, "aarch64": ArchARM64,
		"386": Arch386, "X86": Arch386, "i686": Arch386,
		"arm": ArchARM, "ARM": ArchARM, "armv7l": ArchARM,
		"js": ArchJS, "JS": ArchJS,
		"wasip1": ArchWasip1, "WASIP1": ArchWasip1,
	}
	for in, want := range cases {
		got, ok := NormalizeArch(in)
		assert.True(t, ok, "expected %q to normalize", in)
		assert.Equal(t, want, got, "for input %q", in)
	}

	// No bare "wasi" alias: WASI snapshots are versioned (wasip1, wasip2 in
	// the wings) and an unversioned alias would change meaning later. And
	// "wasm" is the OS (the arch names the flavor: js/wasip1), never the arch.
	for _, in := range []string{"", "any", "mips", "riscv64", "wasi", "wasm"} {
		_, ok := NormalizeArch(in)
		assert.False(t, ok, "expected %q to be unrecognized", in)
	}
}
