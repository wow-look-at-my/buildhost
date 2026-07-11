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
// PREVIOUS buildhost versions materialized throwaway tap snapshots into. Taps
// are now served from the persistent per-lineage history store (taphistory.go);
// this constant remains only so resetTapCache can sweep the leftovers an
// upgraded deployment still carries.
const tapSnapshotDirName = "brew-tap"

// tapCacheTTL bounds how long one lineage's serving state is trusted before
// the next request re-checks the database and (when content changed) appends a
// commit. It amortizes the build across the many object GETs of one dumb-HTTP
// `brew update`. Unlike the old throwaway-snapshot design, consistency does
// NOT depend on it: the lineage store is append-only, so a client that read
// refs from build X can always still fetch X's objects even after a publish
// lands mid-update. Package-level var so tests can shorten it.
var tapCacheTTL = 30 * time.Second

// tapCacheMaxEntries caps how many lineages hold a LIVE os.Root at once (open
// directory fds). Distinct entries come from distinct (request base URL,
// credential scope) pairs, so legitimate deployments need a handful; the cap
// keeps a client spraying made-up Host headers from growing the fd table
// inside one TTL window. Evicting the oldest is always safe -- the history
// stays on disk and the next request just reopens it.
const tapCacheMaxEntries = 32

// errTapBuild marks a lineage BUILD failure (500), as opposed to a requested
// path simply not existing in a healthy lineage (404).
var errTapBuild = errors.New("build tap lineage")

// tapLineage is one lineage's live serving state: an os.Root confined to its
// persistent history directory (the same sandbox pattern internal/storage
// uses) plus the time of the last content check. Requests are served by
// opening files through root and memory-mapping them, so a request path can
// never escape the lineage and serving never heap-buffers a whole file.
type tapLineage struct {
	dir     string
	root    *os.Root
	key     string // (base URL, credential scope) the contents derive from
	builtAt time.Time
}

// openTapFile resolves the lineage for the request's (base URL, credential
// scope) cache key -- refreshing it under the mutex when there is no live
// entry -- and opens the requested file inside it. The base URL is part of the
// key because the host is baked into formula download URLs; the credential
// scope (tapScopeKey) is part of the key because tap contents depend on which
// projects the credential may read, so one lineage can never be served across
// scopes. A cache hit does no DB work at all.
//
// Race handling: the open happens while tapMu is still held, and lineage
// removal (the disk-cap eviction) only ever happens under the same mutex, so a
// reader can never resolve a lineage and then find the directory deleted. Once
// the fd is returned, POSIX unlink-while-open semantics keep the file (and any
// mapping of it) readable even after a later eviction removes its directory.
//
// A build failure is reported wrapped in errTapBuild; any other error means
// the requested path does not exist in the (healthy) lineage.
func (h *Handler) openTapFile(r *http.Request, path string) (*os.File, error) {
	key := auth.RequestRootURL(r) + "\x00" + tapScopeKey(r.Context())

	h.tapMu.Lock()
	defer h.tapMu.Unlock()

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
	return lin.root.Open(path)
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
// lineage is new on disk, the store-wide lineage cap is enforced first. Must
// be called with tapMu held.
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
// history root is deliberately NOT removed -- it is what guarantees the tap's
// refs only ever fast-forward across restarts and redeploys; only crash
// orphans are cleaned: temp files inside lineage dirs, plus the whole legacy
// {TmpDir}/brew-tap snapshot root that pre-history versions materialized.
func (h *Handler) resetTapCache() {
	h.tapMu.Lock()
	defer h.tapMu.Unlock()
	for key := range h.tapSnaps {
		h.dropTapLineageLocked(key)
	}
	os.RemoveAll(h.tapRoot())
	sweepTapTempFiles(h.tapHistoryRoot())
}

// dropTapLineageLocked closes one live entry. The on-disk history is never
// touched here -- disk removal happens only in evictTapLineagesLocked. Must be
// called with tapMu held.
func (h *Handler) dropTapLineageLocked(key string) {
	lin := h.tapSnaps[key]
	if lin == nil {
		return
	}
	lin.root.Close()
	delete(h.tapSnaps, key)
}

// tapRoot returns the LEGACY scratch directory old snapshots lived under, kept
// only for resetTapCache's upgrade sweep. TmpDir is always set in production
// ({DataDir}/tmp, wired in OnReady); the OS temp dir fallback mirrors
// repackage.Input.TmpDir's convention for bare test constructions.
func (h *Handler) tapRoot() string {
	base := h.TmpDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, tapSnapshotDirName)
}
