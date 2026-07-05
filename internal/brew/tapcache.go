package brew

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

// tapSnapshotDirName is the directory under the scratch root (TmpDir) that
// holds materialized tap snapshots, one subdirectory per build.
const tapSnapshotDirName = "brew-tap"

// tapCacheTTL bounds how long one built tap snapshot is served before the next
// request rebuilds it. Beyond amortizing the build across the many object GETs
// of one dumb-HTTP `brew update`, the snapshot fixes a real consistency race:
// without it, a publish landing mid-update let a client fetch refs from build X
// and loose objects from build Y, missing objects X referenced. Package-level
// var so tests can shorten it.
var tapCacheTTL = 30 * time.Second

// errTapBuild marks a snapshot BUILD failure (500), as opposed to a requested
// path simply not existing in a healthy snapshot (404).
var errTapBuild = errors.New("build tap snapshot")

// tapSnapshot is one fully built tap, materialized on disk in the dumb-HTTP
// git layout (HEAD, info/refs, refs/heads/main, objects/xx/yyyy...). Requests
// are served by opening files through root -- an os.Root confined to dir, the
// same sandbox pattern internal/storage uses -- and memory-mapping them, so a
// request path can never escape the snapshot and serving never heap-buffers a
// whole file.
type tapSnapshot struct {
	dir     string
	root    *os.Root
	key     string // base URL the build derived its formulas from
	builtAt time.Time
}

// openTapFile resolves the current snapshot for the request -- rebuilding it
// under the mutex when there is none, it expired, or it was built for a
// different base URL (the host is baked into formula download URLs, so a
// cached tap must never be served with the wrong host) -- and opens the
// requested file inside it.
//
// Race handling: the open happens while tapMu is still held, and snapshot
// removal only ever happens under the same mutex (in the swap below), so a
// reader can never resolve a pointer and then find the directory deleted. Once
// the fd is returned, POSIX unlink-while-open semantics keep the file (and any
// mapping of it) readable even after a later rebuild swaps the snapshot out
// and removes its directory. This is why the swap can delete the old snapshot
// immediately instead of keeping previous generations around.
//
// A build failure is reported wrapped in errTapBuild; any other error means
// the requested path does not exist in the (healthy) snapshot.
func (h *Handler) openTapFile(r *http.Request, path string) (*os.File, error) {
	key := auth.RequestRootURL(r)

	h.tapMu.Lock()
	defer h.tapMu.Unlock()

	snap := h.tapSnap
	if snap == nil || snap.key != key || time.Since(snap.builtAt) >= tapCacheTTL {
		fresh, err := h.buildTapSnapshot(r, key)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errTapBuild, err)
		}
		h.dropTapSnapshotLocked()
		h.tapSnap = fresh
		snap = fresh
	}
	return snap.root.Open(path)
}

// buildTapSnapshot materializes one full tap build as real files under
// {TmpDir}/brew-tap/<random>/ and returns it with an opened os.Root for
// sandboxed serving. The caller publishes it by swapping the handler's pointer
// under tapMu once the build has fully completed, so a half-written directory
// is never visible to requests; a directory orphaned by a crash mid-build is
// swept by the next process's resetTapCache.
func (h *Handler) buildTapSnapshot(r *http.Request, key string) (*tapSnapshot, error) {
	repo, err := h.buildTapRepo(r)
	if err != nil {
		return nil, err
	}

	base := h.tapRoot()
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(base, "snap-")
	if err != nil {
		return nil, err
	}
	for name, data := range repo {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &tapSnapshot{dir: dir, root: root, key: key, builtAt: time.Now()}, nil
}

// resetTapCache drops the cached snapshot and removes the whole snapshot root
// on disk. Called whenever the handler is (re)wired to a data dir -- at process
// start that doubles as the sweep of snapshot directories a previous process
// left behind (nothing can hold them open across a restart; snapshots are pure
// caches, rebuilt on the next tap request).
func (h *Handler) resetTapCache() {
	h.tapMu.Lock()
	defer h.tapMu.Unlock()
	h.dropTapSnapshotLocked()
	os.RemoveAll(h.tapRoot())
}

// dropTapSnapshotLocked closes and deletes the current snapshot, if any. Must
// be called with tapMu held. Deleting while requests still hold open fds or
// mappings into the directory is safe on the platforms buildhost targets: the
// inodes stay alive until the last close/unmap.
func (h *Handler) dropTapSnapshotLocked() {
	if h.tapSnap == nil {
		return
	}
	h.tapSnap.root.Close()
	os.RemoveAll(h.tapSnap.dir)
	h.tapSnap = nil
}

// tapRoot returns the directory snapshots live under. TmpDir is always set in
// production ({DataDir}/tmp, wired in OnReady); the OS temp dir fallback
// mirrors repackage.Input.TmpDir's convention for bare test constructions.
func (h *Handler) tapRoot() string {
	base := h.TmpDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, tapSnapshotDirName)
}
