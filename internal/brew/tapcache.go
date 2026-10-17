package brew

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/auth"
)

const tapSnapshotDirName = "brew-tap"

var tapCacheTTL = 30 * time.Second

// tapRefreshWait bounds how long a request that has a tip to serve waits for its lineage's refresh.
var tapRefreshWait = 2 * time.Second

// tapRefresher advances a lineage. A test swaps it to control a refresh's duration.
var tapRefresher = (*Handler).refreshTapLineage

const tapCacheMaxEntries = 32

var errTapBuild = errors.New("build tap lineage")

type tapLineage struct {
	dir     string
	root    *os.Root
	key     string // (base URL.
	builtAt time.Time
	// err is the last refresh's failure. Every request answers it until the next refresh, one tapCacheTTL later, succeeds.
	err error
}

// tapRefresh is one in-flight refresh of a lineage directory. err is set
// before done closes.
type tapRefresh struct {
	done chan struct{}
	err  error
}

// openTapFile resolves the lineage for the request's (base URL, credential
// scope) cache key and opens the requested file inside it.
func (h *Handler) openTapFile(r *http.Request, path string) (*os.File, error) {
	key, err := h.awaitTapLineage(r)
	if err != nil {
		return nil, err
	}
	h.tapMu.Lock()
	defer h.tapMu.Unlock()

	lin, err := h.liveTapLineageLocked(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errTapBuild, err)
	}
	return lin.root.Open(path)
}

// tapLineageKey is the request's (base URL, credential scope) cache key.
func tapLineageKey(r *http.Request) string {
	return auth.RequestRootURL(r) + "\x00" + tapScopeKey(r.Context())
}

// awaitTapLineage makes the request's lineage servable and returns its key.
// A lineage whose last refresh is older than tapCacheTTL gets one started,
// unless one is already running. A lineage with no tip yet waits for that
// refresh to finish. A lineage with a tip waits at most tapRefreshWait, then
// serves the tip it has. A refresh that fails is an error for every request
// that waits on it. Every later request gets that error too, until the next
// refresh, one tapCacheTTL later, succeeds. The refresh runs detached from the request, so a
// client that hangs up never throws its work away. It never holds tapMu, so
// a refresh of one lineage never stalls a request for another. Build
// failures are wrapped in errTapBuild.
func (h *Handler) awaitTapLineage(r *http.Request) (string, error) {
	key := tapLineageKey(r)

	h.tapMu.Lock()
	lin, err := h.liveTapLineageLocked(key)
	if err != nil {
		h.tapMu.Unlock()
		return "", fmt.Errorf("%w: %w", errTapBuild, err)
	}
	ref := h.tapRefreshes[lin.dir]
	if ref == nil && time.Since(lin.builtAt) >= tapCacheTTL {
		ref = h.startTapRefreshLocked(r, lin)
	}
	failed := lin.err
	dir := lin.dir
	h.tapMu.Unlock()

	if ref == nil {
		if failed != nil {
			return "", fmt.Errorf("%w: %w", errTapBuild, failed)
		}
		return key, nil
	}
	var timeout <-chan time.Time
	if readTapTip(dir) != "" {
		timer := time.NewTimer(tapRefreshWait)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-ref.done:
		if ref.err != nil {
			return "", fmt.Errorf("%w: %w", errTapBuild, ref.err)
		}
	case <-timeout:
		if failed != nil {
			return "", fmt.Errorf("%w: %w", errTapBuild, failed)
		}
	case <-r.Context().Done():
		return "", r.Context().Err()
	}
	return key, nil
}

// liveTapLineageLocked returns the live entry for key, creating it over the
// key's lineage directory when there is none. A new entry has no builtAt, so
// the next awaitTapLineage refreshes it. Must be called with tapMu held.
func (h *Handler) liveTapLineageLocked(key string) (*tapLineage, error) {
	if lin := h.tapSnaps[key]; lin != nil {
		return lin, nil
	}
	h.capTapLineagesLocked()
	dir := h.tapLineageDir(key)
	if _, err := os.Stat(dir); err != nil {
		h.evictTapLineagesLocked()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	lin := &tapLineage{dir: dir, root: root, key: key}
	if h.tapSnaps == nil {
		h.tapSnaps = map[string]*tapLineage{}
	}
	h.tapSnaps[key] = lin
	return lin, nil
}

// startTapRefreshLocked starts refreshing lin's directory in the background.
// The directory stays pinned against eviction until the refresh ends. The
// entry's builtAt then moves to the end of the refresh. Its err records the
// the outcome. An entry dropped meanwhile keeps neither: its replacement
// refreshes again.
// Must be called with tapMu held.
func (h *Handler) startTapRefreshLocked(r *http.Request, lin *tapLineage) *tapRefresh {
	ref := &tapRefresh{done: make(chan struct{})}
	if h.tapRefreshes == nil {
		h.tapRefreshes = map[string]*tapRefresh{}
	}
	if h.tapPins == nil {
		h.tapPins = map[string]int{}
	}
	dir := lin.dir
	h.tapRefreshes[dir] = ref
	h.tapPins[dir]++

	bg := r.Clone(context.WithoutCancel(r.Context()))
	bg.Body = http.NoBody
	go func() {
		err := tapRefresher(h, bg, dir)
		if err != nil {
			slog.Warn("refresh brew tap lineage", "dir", dir, "err", err)
		}
		h.tapMu.Lock()
		ref.err = err
		delete(h.tapRefreshes, dir)
		h.unpinTapDirLocked(dir)
		if h.tapSnaps[lin.key] == lin {
			lin.builtAt = time.Now()
			lin.err = err
		}
		h.tapMu.Unlock()
		close(ref.done)
	}()
	return ref
}

func (h *Handler) unpinTapDirLocked(dir string) {
	if h.tapPins[dir] <= 1 {
		delete(h.tapPins, dir)
		return
	}
	h.tapPins[dir]--
}

// acquireTapLineage resolves the request's lineage exactly like openTapFile
// and hands back an INDEPENDENT os.Root over its directory plus a release
func (h *Handler) acquireTapLineage(r *http.Request) (*os.Root, func(), error) {
	key, err := h.awaitTapLineage(r)
	if err != nil {
		return nil, nil, err
	}
	h.tapMu.Lock()
	defer h.tapMu.Unlock()

	lin, err := h.liveTapLineageLocked(key)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errTapBuild, err)
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
		h.unpinTapDirLocked(dir)
		h.tapMu.Unlock()
		root.Close()
	}
	return root, release, nil
}

// capTapLineagesLocked closes the oldest live entries while the map is at its
// cap (the on-disk history stays). Must be called with tapMu held.
func (h *Handler) capTapLineagesLocked() {
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

// tapRoot returns the scratch directory old snapshots lived under, kept
func (h *Handler) tapRoot() string {
	base := h.TmpDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, tapSnapshotDirName)
}
