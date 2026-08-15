package goproxy

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// The zip is the part with no second chance: a module zip that is merely close
// to canonical is a checksum failure at every consumer, and nothing upstream of
// the go command will tell you why. So the served bytes are round-tripped
// through x/mod's own Unzip, which applies the same rules the go command does.
func TestServedZipIsAValidModuleZip(t *testing.T) {
	fake := newFakeGitHub(t)
	modPath := privateOrg + "/tml"
	seedModule(fake, modPath, "", "v1.2.0", "aaaa111122223333444455556666777788889999",
		"module "+modPath+"\n\ngo 1.25\n")
	fake.TreeFiles["README.md"] = "# tml\n"
	fake.TreeFiles["internal/deep/file.go"] = "package deep\n"

	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+modPath+"/@v/v1.2.0.zip")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	zipPath := filepath.Join(t.TempDir(), "mod.zip")
	require.NoError(t, os.WriteFile(zipPath, rec.Body.Bytes(), 0o644))

	mv := module.Version{Path: modPath, Version: "v1.2.0"}

	// CheckZip is exactly what the go command runs before trusting a zip.
	ck, err := modzip.CheckZip(mv, zipPath)
	require.NoError(t, err)
	assert.Empty(t, ck.Invalid, "zip carried files the module spec rejects")

	dest := filepath.Join(t.TempDir(), "unzipped")
	require.NoError(t, modzip.Unzip(dest, mv, zipPath))

	for _, want := range []string{"go.mod", "lib.go", "README.md", "internal/deep/file.go"} {
		_, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want)))
		assert.NoError(t, err, "expected %s in the module zip", want)
	}
}

// A nested module's zip must contain that subdirectory's files at the module
// root, with the parent repo's other directories excluded entirely.
func TestNestedModuleZipContainsOnlyItsSubtree(t *testing.T) {
	fake := newFakeGitHub(t)
	modPath := privateOrg + "/agentic-loop/go"
	seedModule(fake, modPath, "go", "go/v0.3.0", "bbbb111122223333444455556666777788889999",
		"module "+modPath+"\n\ngo 1.25\n")
	fake.TreeFiles["go/inner/thing.go"] = "package inner\n"
	// Siblings of the module directory must not travel with it.
	fake.TreeFiles["README.md"] = "# repo\n"
	fake.TreeFiles["python/main.py"] = "print('no')\n"

	s := newTestService(t, fake, "tok", []string{privateOrg})

	rec := serveProxy(t, s, "/"+modPath+"/@v/v0.3.0.zip")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	zipPath := filepath.Join(t.TempDir(), "mod.zip")
	require.NoError(t, os.WriteFile(zipPath, rec.Body.Bytes(), 0o644))

	mv := module.Version{Path: modPath, Version: "v0.3.0"}
	require.NoError(t, func() error { _, err := modzip.CheckZip(mv, zipPath); return err }())

	dest := filepath.Join(t.TempDir(), "unzipped")
	require.NoError(t, modzip.Unzip(dest, mv, zipPath))

	for _, want := range []string{"go.mod", "lib.go", "inner/thing.go"} {
		_, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want)))
		assert.NoError(t, err, "expected %s in the nested module zip", want)
	}
	for _, unwanted := range []string{"README.md", "python/main.py"} {
		_, err := os.Stat(filepath.Join(dest, filepath.FromSlash(unwanted)))
		assert.True(t, os.IsNotExist(err), "%s belongs to the repo, not this module", unwanted)
	}
}

// A repository tarball is untrusted input, and a "../" entry in one is how an
// archive writes outside the directory it is being extracted into.
func TestTarballCannotEscapeTheExtractionRoot(t *testing.T) {
	dir := t.TempDir()
	_, err := safeJoin(dir, "../escaped")
	require.Error(t, err)
	_, err = safeJoin(dir, "a/../../escaped")
	require.Error(t, err)

	got, err := safeJoin(dir, "a/b/c.go")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "a", "b", "c.go"), got)
}
