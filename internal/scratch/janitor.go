// scratchPrefixes are the temporary-file names this server creates and then removes itself.

package scratch

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// scratchPrefixes are the temporary-file names this server creates and then
// removes itself. A file left behind by a crash carries one of these names.
var scratchPrefixes = []string{
	"strip-input-",
	"stripped-",
	"debug-",
	"site-zip-",
	"site-archive-",
	"site-convert-",
	"deb-data-",
	"goproxy-zip-",
}

// scratchDirPrefixes are temporary DIRECTORIES with the same property.
var scratchDirPrefixes = []string{"goproxy-src-"}

// keptEntries are the durable spool directories an earlier version kept
// beside the scratch root.
var keptEntries = map[string]bool{
	"uploads":     true,
	"oci-uploads": true,
}

// Janitor reclaims orphaned temporary files and expired cache entries.
type Janitor struct {
	// Root is the active scratch root.
	Root string
	// LegacyDir is the {DataDir}/tmp an earlier version wrote scratch into. Empty skips it.
	LegacyDir string
	// CacheDir is the compressed-response cache directory.
	CacheDir string
	// StaleAge is how old a file must be before it is orphaned.
	StaleAge time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// SweepReport is what one sweep removed.
type SweepReport struct {
	Files   int
	Dirs    int
	Bytes   int64
	Scanned int
}

func (j *Janitor) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// Run removes orphaned scratch files and expired cache entries from the
// active root and the data-volume directory. A file is orphaned when it
// carries a scratch name and predates the stale age. Upload spools are never
// touched: only names this server creates are eligible.
func (j *Janitor) Run() SweepReport {
	var rep SweepReport
	cutoff := j.now().Add(-j.StaleAge)
	for _, dir := range []string{j.Root, j.LegacyDir} {
		if dir == "" {
			continue
		}
		removed := j.sweepDir(dir, cutoff)
		rep.Files += removed.Files
		rep.Dirs += removed.Dirs
		rep.Bytes += removed.Bytes
		rep.Scanned += removed.Scanned
	}
	if j.CacheDir != "" {
		removed := j.sweepCache(cutoff)
		rep.Files += removed.Files
		rep.Bytes += removed.Bytes
		rep.Scanned += removed.Scanned
	}
	if rep.Files > 0 || rep.Dirs > 0 {
		slog.Info("scratch: reclaimed orphaned temporary files",
			"files", rep.Files, "dirs", rep.Dirs, "bytes", rep.Bytes)
	}
	return rep
}

func (j *Janitor) sweepDir(dir string, cutoff time.Time) SweepReport {
	var rep SweepReport
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("scratch: listing temporary directory failed", "dir", dir, "err", err)
		}
		return rep
	}
	for _, e := range entries {
		name := e.Name()
		if keptEntries[name] {
			continue
		}
		rep.Scanned++
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		switch {
		case e.IsDir() && hasPrefix(name, scratchDirPrefixes):
			if err := os.RemoveAll(path); err == nil {
				rep.Dirs++
			}
		case info.Mode().IsRegular() && hasPrefix(name, scratchPrefixes):
			if err := os.Remove(path); err == nil {
				rep.Files++
				rep.Bytes += info.Size()
			}
		}
	}
	return rep
}

func (j *Janitor) sweepCache(cutoff time.Time) SweepReport {
	var rep SweepReport
	entries, err := os.ReadDir(j.CacheDir)
	if err != nil {
		return rep
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		rep.Scanned++
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(j.CacheDir, e.Name())); err == nil {
			rep.Files++
			rep.Bytes += info.Size()
		}
	}
	return rep
}

func hasPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
