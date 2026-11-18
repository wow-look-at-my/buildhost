package web_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// invisible are the code points a person can not see and a shell does not
// ignore. One of them at the end of a copied command becomes part of a file
// name or an argument.
var invisible = []struct {
	lo, hi rune
	name   string
}{
	{0x00AD, 0x00AD, "soft hyphen"},
	{0x200B, 0x200F, "zero-width or direction mark"},
	{0x2028, 0x202E, "line separator or direction override"},
	{0x2060, 0x2064, "word joiner or invisible operator"},
	{0xFEFF, 0xFEFF, "byte order mark"},
}

func invisibleName(r rune) string {
	for _, rng := range invisible {
		if r >= rng.lo && r <= rng.hi {
			return rng.name
		}
	}
	return ""
}

// repoRoot is the directory that holds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "no go.mod above the test")
		dir = parent
	}
}

// findInvisible lists the invisible code points of a text, by line.
func findInvisible(data []byte) []string {
	var out []string
	for n, line := range bytes.Split(data, []byte("\n")) {
		for _, r := range string(line) {
			if name := invisibleName(r); name != "" {
				out = append(out, fmt.Sprintf("line %d: U+%04X (%s)", n+1, r, name))
			}
		}
	}
	return out
}

func TestFindInvisibleNamesTheCodePoint(t *testing.T) {
	text := "curl -O x" + string(rune(0x200B)) + "\nplain\n" + string(rune(0xFEFF)) + "y"
	assert.Equal(t, []string{
		"line 1: U+200B (zero-width or direction mark)",
		"line 3: U+FEFF (byte order mark)",
	}, findInvisible([]byte(text)))
	assert.Empty(t, findInvisible([]byte("an em dash — and an arrow → are visible")))
}

// Every text file of the repository is a file the server can send, or a
// file a person copies a command from. None may hold an invisible code point.
func TestNoFileHoldsAnInvisibleCharacter(t *testing.T) {
	root := repoRoot(t)
	skip := set.Of[string](".git", "node_modules", "build")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip.Contains(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			// A file with a NUL is not text.
			return nil
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		for _, hit := range findInvisible(data) {
			t.Errorf("%s, %s", rel, hit)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, checked, 100, "the walk must reach the repository, not an empty directory")
}
