// Package recompress caches compressed download responses and bounds how much
// on-the-fly compression one client may cause.
package recompress

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Key derives a cache entry name from every input that changes the stored
// bytes: the artifact, the payload transform version, the transfer coding and
// the debug flag.
func Key(storageKey, transform, encoding string, debug bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%t", transform, storageKey, encoding, debug)
	return hex.EncodeToString(h.Sum(nil))
}

type cacheEntry struct {
	size    int64
	created time.Time
	used    time.Time
}

// Cache is a size and count bounded LRU of recompressed downloads. Entries are
// keyed by Key and are never rewritten while they live, so repeated requests
// see identical bytes.
type Cache struct {
	dir     string
	limit   int64
	entries int
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	index map[string]*cacheEntry
	bytes int64
}

// NewCache opens the cache in dir, creating it when missing. Entries already
// past their TTL are removed.
func NewCache(dir string, limit int64, maxEntries int, ttl time.Duration) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create recompress cache: %w", err)
	}
	c := &Cache{
		dir:     dir,
		limit:   limit,
		entries: maxEntries,
		ttl:     ttl,
		now:     time.Now,
		index:   map[string]*cacheEntry{},
	}
	c.load()
	return c, nil
}

// Dir is where entries live.
func (c *Cache) Dir() string { return c.dir }

func (c *Cache) load() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !validKey(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c.index[e.Name()] = &cacheEntry{size: info.Size(), created: info.ModTime(), used: info.ModTime()}
		c.bytes += info.Size()
	}
	c.mu.Lock()
	c.evictLocked(c.now(), "")
	c.mu.Unlock()
}

func validKey(name string) bool {
	if len(name) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func (c *Cache) path(key string) string { return filepath.Join(c.dir, key) }

// Create returns a temporary file inside the cache directory. Insert promotes
// it atomically, so a reader never sees a half-written entry.
func (c *Cache) Create() (*os.File, error) {
	f, err := os.CreateTemp(c.dir, "incoming-*")
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

// Insert promotes a finished temporary file to key and returns its size. Only
// a successfully produced variant reaches here, so every entry is complete.
func (c *Cache) Insert(key, tmpPath string) (int64, error) {
	info, err := os.Stat(tmpPath)
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if c.limit > 0 && size > c.limit {
		return size, nil
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return size, err
	}
	if err := os.Rename(tmpPath, c.path(key)); err != nil {
		return size, err
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.index[key]; ok {
		c.bytes -= old.size
	}
	c.index[key] = &cacheEntry{size: size, created: now, used: now}
	c.bytes += size
	c.evictLocked(now, key)
	return size, nil
}

// Open returns a cached entry for reading and marks it most recently used. A
// miss, an expired entry and an unreadable file all report false.
func (c *Cache) Open(key string) (*os.File, int64, bool) {
	c.mu.Lock()
	e, ok := c.index[key]
	if !ok {
		c.mu.Unlock()
		return nil, 0, false
	}
	now := c.now()
	if c.ttl > 0 && now.Sub(e.created) > c.ttl {
		c.dropLocked(key)
		c.mu.Unlock()
		os.Remove(c.path(key))
		return nil, 0, false
	}
	e.used = now
	size := e.size
	c.mu.Unlock()

	f, err := os.Open(c.path(key))
	if err != nil {
		c.mu.Lock()
		c.dropLocked(key)
		c.mu.Unlock()
		os.Remove(c.path(key))
		return nil, 0, false
	}
	return f, size, true
}

// Len reports how many entries the index holds.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.index)
}

// Bytes reports the size of the indexed entries.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

func (c *Cache) dropLocked(key string) {
	if entry, ok := c.index[key]; ok {
		delete(c.index, key)
		c.bytes -= entry.size
	}
}

// evictLocked drops expired entries and then the least recently used until the
// size and count bounds hold. keep is never a victim.
func (c *Cache) evictLocked(now time.Time, keep string) {
	for k, e := range c.index {
		if c.ttl > 0 && now.Sub(e.created) > c.ttl {
			delete(c.index, k)
			c.bytes -= e.size
			os.Remove(c.path(k))
		}
	}
	for c.over() {
		victim := ""
		var oldest time.Time
		for k, e := range c.index {
			if k == keep {
				continue
			}
			if victim == "" || e.used.Before(oldest) {
				victim, oldest = k, e.used
			}
		}
		if victim == "" {
			return
		}
		e := c.index[victim]
		delete(c.index, victim)
		c.bytes -= e.size
		os.Remove(c.path(victim))
	}
}

// over reports whether an index exceeds either bound.
func (c *Cache) over() bool {
	return (c.limit > 0 && c.bytes > c.limit) || (c.entries > 0 && len(c.index) > c.entries)
}
