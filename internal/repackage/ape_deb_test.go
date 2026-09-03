package repackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apeFixture is a file shaped like a Cosmopolitan APE: the MZqFpD prologue,
// and (like the real thing) it writes to its own path before doing its job, so
func apeFixture() []byte {
	return []byte("MZqFpD='fixture'\n" +
		`: >> "$0" || { echo "self-write failed" >&2; exit 1; }` + "\n" +
		"echo ape-deb-ok\n")
}

func buildDeb(t *testing.T, payload []byte, kind db.Kind, name string) []byte {
	t.Helper()
	out, err := (&Deb{}).Repackage(context.Background(), Input{
		Project:  db.Project{Name: name},
		Release:  db.Release{Version: "1.2.3", VersionNum: 1},
		Artifact: db.Artifact{OS: db.OSLinux, Arch: db.ArchAMD64, Kind: kind},
		Reader:   bytes.NewReader(payload),
		Size:     int64(len(payload)),
		TmpDir:   t.TempDir(),
	})
	require.NoError(t, err)
	blob, err := io.ReadAll(out.Reader)
	require.NoError(t, err)
	require.NoError(t, out.Reader.Close())
	return blob
}

// debEntries extracts data.tar.gz's entries via `dpkg-deb`, so the test reads
// the archive the same way dpkg does rather than trusting our own parser.
func debEntries(t *testing.T, deb []byte) map[string]string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pkg.deb")
	require.NoError(t, os.WriteFile(path, deb, 0o644))

	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not available")
	}
	raw, err := exec.Command("dpkg-deb", "--fsys-tarfile", path).Output()
	require.NoError(t, err)

	entries := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		body, _ := io.ReadAll(tr)
		entries[h.Name] = string(body)
	}
	return entries
}

// A Cosmopolitan APE cannot run from a root-owned /usr/bin entry: it rewrites
func TestDeb_APEBinaryGetsLauncher(t *testing.T) {
	entries := debEntries(t, buildDeb(t, apeFixture(), db.KindBinary, "go-toolchain"))

	// dpkg creates no leading directories itself: without this entry the
	assert.Contains(t, entries, "./usr/lib/go-toolchain/")
	assert.Equal(t, string(apeFixture()), entries["./usr/lib/go-toolchain/go-toolchain"])

	launcher, ok := entries["./usr/bin/go-toolchain"]
	require.True(t, ok, "an APE package must ship a /usr/bin launcher")
	assert.Contains(t, launcher, "#!/bin/sh")
	assert.Contains(t, launcher, "/usr/lib/go-toolchain/go-toolchain")
	// Version-keyed cache path: an upgrade gets a fresh copy with no staleness check.
	assert.Contains(t, launcher, "buildhost/go-toolchain/1.2.3")
	// Never executes the packaged binary as root at install time.
	assert.NotContains(t, launcher, "sudo")
}

// The launcher is generated shell, so run it: with the real binary read-only
// (the dpkg install shape), a per-user copy must be made and executed.
func TestDeb_APELauncherRunsReadOnlyBinary(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell")
	}
	root := t.TempDir()
	real := filepath.Join(root, "usr", "lib", "tool", "tool")
	require.NoError(t, os.MkdirAll(filepath.Dir(real), 0o755))
	require.NoError(t, os.WriteFile(real, apeFixture(), 0o555)) // read-only, as installed

	launcher := filepath.Join(root, "launcher")
	rendered, err := debAPELauncher("tool", "1.2.3")
	require.NoError(t, err)
	script := strings.ReplaceAll(rendered, "'/usr/lib/tool/tool'", "'"+real+"'")
	require.NoError(t, os.WriteFile(launcher, []byte(script), 0o755))

	cache := filepath.Join(root, "cache")
	cmd := exec.Command("sh", launcher)
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "launcher must run a read-only APE: %s", out)
	assert.Equal(t, "ape-deb-ok\n", string(out))

	copied := filepath.Join(cache, "buildhost", "tool", "1.2.3", "tool")
	fi, err := os.Stat(copied)
	require.NoError(t, err, "launcher must leave a per-user copy")
	assert.NotZero(t, fi.Mode()&0o200, "the copy must be writable: an APE rewrites itself")
}

// Everything that is not an APE keeps the previous layout exactly: straight to
func TestDeb_NonAPEBinaryLayoutUnchanged(t *testing.T) {
	entries := debEntries(t, buildDeb(t, []byte("\x7fELF plain binary"), db.KindBinary, "plain"))

	require.Contains(t, entries, "./usr/bin/plain")
	assert.Equal(t, "\x7fELF plain binary", entries["./usr/bin/plain"])
	assert.Len(t, entries, 1, "a non-APE binary package has exactly one entry: %v", entries)
}

// The directory-entry fix is not APE-specific: library and assets packages
// install outside /usr/bin too, and were equally unpackable without it.
func TestDeb_NestedInstallDirsCarryDirectoryEntry(t *testing.T) {
	for _, tc := range []struct {
		kind db.Kind
		dir  string
	}{
		{db.KindLibrary, "./usr/lib/mylib/"},
		{db.KindAssets, "./usr/share/mylib/"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			entries := debEntries(t, buildDeb(t, []byte("payload"), tc.kind, "mylib"))
			assert.Contains(t, entries, tc.dir, "dpkg cannot create this directory itself")
		})
	}
}

// gzip streams carry no timestamps here and the launcher is derived only from
// the package name and version, so the same inputs must produce the same deb --
// the APT Packages index caches its sha256.
func TestDeb_APEGenerationDeterministic(t *testing.T) {
	a := buildDeb(t, apeFixture(), db.KindBinary, "go-toolchain")
	b := buildDeb(t, apeFixture(), db.KindBinary, "go-toolchain")
	assert.Equal(t, a, b)
}

func TestPeekAPE(t *testing.T) {
	for name, tc := range map[string]struct {
		in   []byte
		want bool
	}{
		"ape":            {apeFixture(), true},
		"elf":            {[]byte("\x7fELFrest"), false},
		"script":         {[]byte("#!/bin/sh\n"), false},
		"short":          {[]byte("MZ"), false},
		"empty":          {nil, false},
		"mz but not ape": {[]byte("MZ\x90\x00\x03\x00\x00\x00"), false},
	} {
		t.Run(name, func(t *testing.T) {
			got, rest, err := peekAPE(bytes.NewReader(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			// The stream must be fully replayable: it is the artifact body.
			all, err := io.ReadAll(rest)
			require.NoError(t, err)
			assert.Equal(t, string(tc.in), string(all))
		})
	}
}

func TestDeb_APEDataTarReadableAsGzip(t *testing.T) {
	deb := buildDeb(t, apeFixture(), db.KindBinary, "go-toolchain")
	i := bytes.Index(deb, []byte("data.tar.gz"))
	require.Greater(t, i, 0)
	_, err := gzip.NewReader(bytes.NewReader(deb[i+60:]))
	require.NoError(t, err)
}
