// Package scratch resolves the root for request-path temporary files.
//
// The data volume holds durable state only. Binary stripping, repackaging,
// site archives and the compressed-response cache all write beneath this
// root, which is TMPDIR unless BUILDHOST_TEMP_DIR names another place.
package scratch

import (
	"os"
	"path/filepath"
	"sync"
)

// CacheDirName is the subdirectory of the root holding cached compressed
// responses. The startup janitor owns everything beneath it.
const CacheDirName = "buildhost-recompress"

// ToolDirName is the subdirectory holding the packer extracted from the
// server binary.
const ToolDirName = "buildhost-tools"

var (
	mu     sync.RWMutex
	root   string
	rootOK bool
)

// Configure sets the root. An empty dir means the default.
func Configure(dir string) {
	mu.Lock()
	defer mu.Unlock()
	root = dir
	rootOK = dir != ""
}

// Root returns the scratch root: the configured override when one is set, else
// os.TempDir(), which is $TMPDIR on unix.
func Root() string {
	mu.RLock()
	defer mu.RUnlock()
	if rootOK {
		return root
	}
	return os.TempDir()
}

// Dir returns Root joined with name, creating it when missing.
func Dir(name string) (string, error) {
	p := filepath.Join(Root(), name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}
