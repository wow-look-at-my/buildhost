package brew

// Persistent tap history. Every tap build used to mint a single PARENTLESS
// root commit into a throwaway snapshot under {DataDir}/tmp -- so each publish
// rewrote the tap's entire git history, and Homebrew's updater (which fetches
// with --force and rebases the client's clone onto origin/main) replayed the
// client's old root onto an unrelated new root: add/add conflicts, every
// client wedged mid-rebase after every publish.
//
// This file gives each tap LINEAGE -- one (apex base URL, credential scope)
// pair, exactly what tapcache keys builds by -- a durable, append-only,
// bare-layout git directory under {DataDir}/brew-tap/<sha256(key)>/ (NOT under
// {DataDir}/tmp, which is scratch space; the same durable-state precedent as
// apt-signing.key and download-signing.key):
//
//	objects/xx/yyyy...   loose objects, content-addressed, never rewritten
//	refs/heads/main      the tip commit sha ("<sha>\n"), advanced temp+rename
//	info/refs            "<sha>\trefs/heads/main\n" (dumb-HTTP ref listing)
//	HEAD                 "ref: refs/heads/main\n"
//	objects/info/packs   empty (no packs; loose objects only)
//
// The directory IS the served repo (mmap'd through an os.Root, the storage
// pattern), and it is the durable truth: a rebuild reads the persisted tip,
// reuses it when the new content's tree is unchanged, and otherwise mints a
// commit WITH `parent <tip>` -- so per lineage, refs/heads/main only ever
// moves to a DESCENDANT of its previous value, across restarts and redeploys.
// Objects are only ever added (a publish adds ~2-4 small objects), so a client
// mid-`brew update` can always fetch every object its refs snapshot names even
// if the tip advances underneath it.

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tapHistoryDirName is the directory under the persistent data dir (NOT the
// swept tmp scratch root) that holds one subdirectory per tap lineage.
const tapHistoryDirName = "brew-tap"

// tapHistoryMaxLineages caps how many lineage histories are kept on disk.
// Legitimate deployments need a handful (one per apex host x credential scope
// that actually taps); the cap keeps junk Host headers and deleted tokens from
// growing the store without bound. Eviction is whole-lineage, LRU by directory
// mtime (bumped on every build) -- an evicted lineage restarts from a fresh
// root on its next request, which is acceptable exactly because eviction only
// ever reaches lineages nothing has fetched for a long time.
const tapHistoryMaxLineages = 64

// tapHistoryRoot returns the persistent lineage-store root. Production always
// wires DataDir (OnReady); the fallbacks keep bare test constructions working
// without ever colliding with the legacy tmp snapshot dir.
func (h *Handler) tapHistoryRoot() string {
	if h.DataDir != "" {
		return filepath.Join(h.DataDir, tapHistoryDirName)
	}
	base := h.TmpDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "brew-tap-history")
}

// tapLineageDir maps a tapcache key to its on-disk lineage directory. Hashing
// keeps hostile Host headers / token names from smuggling path syntax into the
// directory name.
func (h *Handler) tapLineageDir(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(h.tapHistoryRoot(), hex.EncodeToString(sum[:]))
}

// refreshTapLineage recomputes the tap contents for the request's scope and
// advances the lineage store at dir. If the new content's tree equals the
// persisted tip's tree the tip commit is REUSED (no growth from the periodic
// TTL rebuilds); otherwise a child commit of the tip is minted, its objects
// are written (content-addressed, idempotent, temp+rename), and only then is
// the tip advanced -- so a reader can never observe a ref naming objects that
// are not yet on disk, and a crash at any point leaves a consistent store.
func (h *Handler) refreshTapLineage(r *http.Request, dir string) error {
	files, err := h.buildTapFiles(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tip := readTapTip(dir)
	objects, commitSHA, treeSHA := buildGitObjects(files, tip)
	if tip != "" {
		if tipTree, err := readCommitTree(dir, tip); err == nil && tipTree == treeSHA {
			// Content unchanged: keep the tip commit -- the sha stays stable
			// across rebuilds. Just record recency for the LRU cap.
			touchTapLineage(dir)
			return nil
		}
		// An unreadable tip OBJECT (external corruption) still keeps tip as
		// the parent: clients that hold the old history keep fast-forwarding,
		// which is the guarantee this store exists for.
	}
	if err := writeTapObjects(dir, objects); err != nil {
		return err
	}
	if err := advanceTapTip(dir, commitSHA); err != nil {
		return err
	}
	touchTapLineage(dir)
	return nil
}

// readTapTip returns the lineage's persisted tip commit sha, or "" when the
// lineage has no history yet (first build seeds a parentless root).
func readTapTip(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "refs", "heads", "main"))
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(b))
	if !validLooseSHA(sha) {
		return ""
	}
	return sha
}

func validLooseSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// readCommitTree parses the "tree <sha>" header out of a stored loose commit
// object -- what the tree-unchanged dedupe compares against.
func readCommitTree(dir, commitSHA string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "objects", commitSHA[:2], commitSHA[2:]))
	if err != nil {
		return "", err
	}
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr) // commits are ~200 bytes
	if err != nil {
		return "", err
	}
	_, body, ok := bytes.Cut(raw, []byte{0})
	if !ok {
		return "", fmt.Errorf("malformed loose object %s", commitSHA)
	}
	tree, ok := strings.CutPrefix(string(body), "tree ")
	if !ok || len(tree) < 40 || !validLooseSHA(tree[:40]) {
		return "", fmt.Errorf("loose object %s carries no tree header", commitSHA)
	}
	return tree[:40], nil
}

// writeTapObjects persists loose objects into the lineage's object store.
// Content-addressed and append-only: an object that already exists is skipped,
// new ones land via temp+rename so a crash never leaves a partial object under
// its final name.
func writeTapObjects(dir string, objects map[string][]byte) error {
	for sha, data := range objects {
		rel := filepath.Join("objects", sha[:2], sha[2:])
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			continue
		}
		if err := writeTapFileAtomic(dir, rel, data); err != nil {
			return err
		}
	}
	return nil
}

// advanceTapTip publishes commitSHA as the lineage's tip. Callers must have
// persisted the commit's objects first. refs/heads/main is written last: it is
// what the next build reads as the parent, so the commit point of a tip move
// is a single atomic rename.
func advanceTapTip(dir, commitSHA string) error {
	if err := writeTapFileAtomic(dir, "HEAD", []byte("ref: refs/heads/main\n")); err != nil {
		return err
	}
	if err := writeTapFileAtomic(dir, filepath.Join("objects", "info", "packs"), nil); err != nil {
		return err
	}
	if err := writeTapFileAtomic(dir, filepath.Join("info", "refs"), []byte(commitSHA+"\trefs/heads/main\n")); err != nil {
		return err
	}
	return writeTapFileAtomic(dir, filepath.Join("refs", "heads", "main"), []byte(commitSHA+"\n"))
}

// writeTapFileAtomic writes dir/name via a temp file in dir + rename. Temp
// names carry the tapTempPrefix so a crash's orphans are recognizably sweepable.
func writeTapFileAtomic(dir, name string, data []byte) error {
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tapTempPrefix+"*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

const tapTempPrefix = ".tmp-"

// touchTapLineage stamps the lineage directory's mtime -- the LRU recency the
// disk cap evicts by. Explicit because a tree-unchanged rebuild writes nothing.
func touchTapLineage(dir string) {
	now := time.Now()
	_ = os.Chtimes(dir, now, now)
}

// evictTapLineagesLocked enforces tapHistoryMaxLineages before a NEW lineage
// directory is created: while at or over the cap, the least-recently-built
// lineage (dir mtime) is dropped whole -- from the in-memory cache too, so its
// os.Root closes -- and its history restarts from a fresh root if it is ever
// requested again. A lineage pinned by an in-flight smart request
// (acquireTapLineage) is never a victim -- a streaming pack walk must not have
// its objects deleted underneath it -- so the cap can be transiently exceeded
// by exactly the number of active requests. Must be called with tapMu held.
func (h *Handler) evictTapLineagesLocked() {
	root := h.tapHistoryRoot()
	for {
		entries, err := os.ReadDir(root)
		if err != nil {
			return
		}
		type cand struct {
			name string
			mod  time.Time
		}
		var dirs []cand
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			dirs = append(dirs, cand{name: e.Name(), mod: info.ModTime()})
		}
		if len(dirs) < tapHistoryMaxLineages {
			return
		}
		oldest := ""
		var oldestMod time.Time
		for _, d := range dirs {
			if h.tapPins[filepath.Join(root, d.name)] > 0 {
				continue
			}
			if oldest == "" || d.mod.Before(oldestMod) {
				oldest, oldestMod = d.name, d.mod
			}
		}
		if oldest == "" {
			return // every candidate is pinned by an in-flight request
		}
		victim := filepath.Join(root, oldest)
		for key, lin := range h.tapSnaps {
			if lin.dir == victim {
				h.dropTapLineageLocked(key)
			}
		}
		if err := os.RemoveAll(victim); err != nil {
			return
		}
	}
}

// sweepTapTempFiles removes writeTapFileAtomic temp files a crashed previous
// process orphaned inside lineage directories. Best-effort, called at wiring.
func sweepTapTempFiles(root string) {
	lineages, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, l := range lineages {
		if !l.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, l.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.HasPrefix(f.Name(), tapTempPrefix) {
				os.Remove(filepath.Join(root, l.Name(), f.Name()))
			}
		}
	}
}
