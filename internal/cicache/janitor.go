// Package cicache evicts the CI cache. An entry nothing restored for Keep
// goes. While free disk is below MinFreePercent, the least recently used
// entries go sooner, until free disk is back above it.
package cicache

import (
	"context"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/diskusage"
	"github.com/wow-look-at-my/buildhost/internal/retention"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

// CheckInterval is how often the janitor runs.
const CheckInterval = 5 * time.Minute

type Janitor struct {
	DB             *db.DB
	Store          storage.Storage
	DataDir        string
	Keep           time.Duration
	MinFreePercent float64

	// Disk reports the filesystem that holds DataDir. See diskusage.Get.
	Disk func(path string) (diskusage.Usage, error)
	// Busy reports writes in flight.
	Busy func() bool
}

// Report is what one pass of the janitor did.
type Report struct {
	At              time.Time `json:"at"`
	DurationMS      int64     `json:"duration_ms"`
	Deferred        bool      `json:"deferred"`
	Entries         int       `json:"entries"`
	Expired         int       `json:"expired"`
	PressureDeleted int       `json:"pressure_deleted"`
	FreedBytes      int64     `json:"freed_bytes"`
	Error           string    `json:"error,omitempty"`
}

var lastReport atomic.Pointer[Report]

// LastReport returns the most recent pass, or nil before the first one.
func LastReport() *Report { return lastReport.Load() }

// Start runs Sweep every CheckInterval until ctx is done.
func (j *Janitor) Start(ctx context.Context) {
	slog.Info("cache janitor enabled", "keep", j.Keep, "min_free_percent", j.MinFreePercent)
	go func() {
		t := time.NewTicker(CheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if j.Busy != nil && j.Busy() {
					slog.Info("cache: deferring sweep, writes in flight")
					lastReport.Store(&Report{At: time.Now(), Deferred: true})
					continue
				}
				if err := j.Sweep(ctx, time.Now()); err != nil {
					slog.Error("cache sweep failed", "err", err)
				}
			}
		}
	}()
}

// Sweep deletes the entries the cache no longer keeps, and records what it
// did for LastReport.
func (j *Janitor) Sweep(ctx context.Context, now time.Time) error {
	started := time.Now()
	rep := &Report{At: now}
	err := j.sweep(ctx, now, rep)
	rep.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		rep.Error = err.Error()
	}
	lastReport.Store(rep)
	return err
}

func (j *Janitor) sweep(ctx context.Context, now time.Time, rep *Report) error {
	entries, err := j.DB.ListCacheEntries(ctx)
	if err != nil {
		return err
	}
	rep.Entries = len(entries)
	var kept []db.CacheEntry
	for _, e := range entries {
		if idle := now.Sub(e.UsedAt); idle >= j.Keep {
			if j.delete(ctx, e, "unused for "+idle.Round(time.Minute).String(), rep) {
				rep.Expired++
			}
			continue
		}
		kept = append(kept, e)
	}
	return j.relievePressure(ctx, kept, rep)
}

// relievePressure deletes entries, least recently used first, until free
// disk is back above MinFreePercent. entries is in that order already.
func (j *Janitor) relievePressure(ctx context.Context, entries []db.CacheEntry, rep *Report) error {
	if j.MinFreePercent <= 0 {
		return nil
	}
	floor := strconv.FormatFloat(j.MinFreePercent, 'f', -1, 64) + "%"
	for _, e := range entries {
		du, err := j.Disk(j.DataDir)
		if err != nil {
			return err
		}
		if du.Total == 0 || float64(du.Free)*100/float64(du.Total) >= j.MinFreePercent {
			return nil
		}
		if j.delete(ctx, e, "free disk is below "+floor, rep) {
			rep.PressureDeleted++
		}
	}
	return nil
}

func (j *Janitor) delete(ctx context.Context, e db.CacheEntry, reason string, rep *Report) bool {
	if err := j.DB.DeleteCache(ctx, db.CacheScope{RepoID: e.RepoID, Ref: e.Ref}, e.Key); err != nil {
		slog.Error("cache: delete entry failed", "repo", e.RepoPath, "ref", e.Ref, "key", e.Key, "err", err)
		return false
	}
	if _, err := retention.DeleteBlobIfUnreferenced(ctx, j.DB, j.Store, e.StorageKey, true); err != nil {
		slog.Error("cache: delete blob failed", "key", e.StorageKey, "err", err)
	}
	rep.FreedBytes += e.Size
	slog.Info("cache entry deleted", "repo", e.RepoPath, "ref", e.Ref, "key", e.Key, "bytes", e.Size, "reason", reason)
	return true
}
