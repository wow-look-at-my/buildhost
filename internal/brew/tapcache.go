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
const tapSnapshotDirName = "brew-tap"

var tapCacheTTL = 30 * time.Second

const tapCacheMaxEntries = 32

var errTapBuild = errors.New("build tap lineage")

type tapLineage struct {
	dir     string
	root    *os.Root
	key     string // (base URL, credential scope) the contents derive from
	builtAt time.Time
}

// openTapFile resolves the lineage for the request's (base URL, credential
// scope) cache key -- refreshing it under the mutex when there is no live
// entry -- and opens the requested file inside it. The base URL is part of the
func (h *Handler) openTapFile(r *http.Request, path string) (*os.File, error) {
	h.tapMu.Lock()
	defer h.tapMu.Unlock()

	lin, err := h.resolveTapLineageLocked(r)
	if err != nil {
		return nil, err
	}
	return lin.root.Open(path)
}

// resolveTapLineageLocked is the shared lineage resolution: sweep expired
// entries, then return the live entry for the request's (base URL, credential
// scope) key, refreshing/building it when there is none. Must be called with
// tapMu held. Build failures are wrapped in errTapBuild.
func (h *Handler) resolveTapLineageLocked(r *http.Request) (*tapLineage, error) {
	key := auth.RequestRootURL(r) + "\x00" + tapScopeKey(r.Context())

	h.sweepTapLineagesLocked()

	lin := h.tapSnaps[key]
	if lin == nil {
		fresh, err := h.buildTapLineageLocked(r, key)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errTapBuild, err)
		}
		if h.tapSnaps == nil {
			h.tapSnaps = map[string]*tapLineage{}
		}
		h.tapSnaps[key] = fresh
		lin = fresh
	}
	return lin, nil
}

// acquireTapLineage resolves the request's lineage exactly like openTapFile
// and hands back an INDEPENDENT os.Root over its directory plus a release
func (h *Handler) acquireTapLineage(r *http.Request) (*os.Root, func(), error) {
	h.tapMu.Lock()
	defer h.tapMu.Unlock()

	lin, err := h.resolveTapLineageLocked(r)
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(lin.dir)
	if err != nil {
		return nil, nil, err
	}
	if h.tapPins == nil {
		h.tapPins = map[string]int{}
	}
	dir := lin.dir
	h.tapPins[dir]++
	release := func() {
		h.tapMu.Lock()
		if h.tapPins[dir] <= 1 {
			delete(h.tapPins, dir)
		} else {
			h.tapPins[dir]--
		}
		h.tapMu.Unlock()
		root.Close()
	}
	return root, release, nil
}

// sweepTapLineagesLocked drops every expired live entry (closing its os.Root;
// the on-disk history stays), then -- should the map still exceed the cap --
// closes the oldest entries. Must be called with tapMu held.
func (h *Handler) sweepTapLineagesLocked() {
	for key, lin := range h.tapSnaps {
		if time.Since(lin.builtAt) >= tapCacheTTL {
			h.dropTapLineageLocked(key)
		}
	}
	for len(h.tapSnaps) >= tapCacheMaxEntries {
		oldestKey := ""
		var oldest time.Time
		for key, lin := range h.tapSnaps {
			if oldestKey == "" || lin.builtAt.Before(oldest) {
				oldestKey, oldest = key, lin.builtAt
			}
		}
		h.dropTapLineageLocked(oldestKey)
	}
}

// buildTapLineageLocked advances the request scope's persistent history (see
// refreshTapLineage: reuse the tip when content is unchanged, else append a
// child commit) and opens an os.Root over it for sandboxed serving. When the
func (h *Handler) buildTapLineageLocked(r *http.Request, key string) (*tapLineage, error) {
	dir := h.tapLineageDir(key)
	if _, err := os.Stat(dir); err != nil {
		h.evictTapLineagesLocked()
	}
	if err := h.refreshTapLineage(r, dir); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &tapLineage{dir: dir, root: root, key: key, builtAt: time.Now()}, nil
}

// resetTapCache drops every live lineage entry and sweeps scratch leftovers.
// Called whenever the handler is (re)wired to a data dir. The persistent
func (h *Handler) resetTapCache() {
	h.tapMu.Lock()
	defer h.tapMu.Unlock()
	for key := range h.tapSnaps {
		h.dropTapLineageLocked(key)
	}
	os.RemoveAll(h.tapRoot())
	sweepTapTempFiles(h.tapHistoryRoot())
}

func (h *Handler) dropTapLineageLocked(key string) {
	lin := h.tapSnaps[key]
	if lin == nil {
		return
	}
	lin.root.Close()
	delete(h.tapSnaps, key)
}

// tapRoot returns the LEGACY scratch directory old snapshots lived under, kept
func (h *Handler) tapRoot() string {
	base := h.TmpDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, tapSnapshotDirName)
}
