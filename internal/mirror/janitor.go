// CheckInterval is how often the janitor runs.

package mirror

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

// Janitor evicts mirrored blobs. A blob nothing read for Keep goes. While free
// disk is below MinFreePercent, the least recently used blobs go sooner.
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
	Blobs           int       `json:"blobs"`
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
	slog.Info("mirror janitor enabled", "keep", j.Keep, "min_free_percent", j.MinFreePercent)
	go func() {
		t := time.NewTicker(CheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if j.Busy != nil && j.Busy() {
					slog.Info("mirror: deferring sweep, writes in flight")
					lastReport.Store(&Report{At: time.Now(), Deferred: true})
					continue
				}
				if err := j.Sweep(ctx, time.Now()); err != nil {
					slog.Error("mirror sweep failed", "err", err)
				}
			}
		}
	}()
}

// Sweep deletes the blobs the mirror no longer keeps, and records what it did for LastReport.
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
	blobs, err := j.DB.ListMirrorBlobs(ctx)
	if err != nil {
		return err
	}
	rep.Blobs = len(blobs)
	var kept []db.MirrorBlob
	for _, b := range blobs {
		if idle := now.Sub(b.UsedAt); idle >= j.Keep {
			if j.delete(ctx, b, "unused for "+idle.Round(time.Minute).String(), rep) {
				rep.Expired++
			}
			continue
		}
		kept = append(kept, b)
	}
	return j.relievePressure(ctx, kept, rep)
}

// relievePressure deletes blobs, least recently used first, until free disk is
// back above MinFreePercent. blobs is in that order already.
func (j *Janitor) relievePressure(ctx context.Context, blobs []db.MirrorBlob, rep *Report) error {
	if j.MinFreePercent <= 0 {
		return nil
	}
	floor := strconv.FormatFloat(j.MinFreePercent, 'f', -1, 64) + "%"
	for _, b := range blobs {
		du, err := j.Disk(j.DataDir)
		if err != nil {
			return err
		}
		if du.Total == 0 || float64(du.Free)*100/float64(du.Total) >= j.MinFreePercent {
			return nil
		}
		if j.delete(ctx, b, "free disk is below "+floor, rep) {
			rep.PressureDeleted++
		}
	}
	return nil
}

func (j *Janitor) delete(ctx context.Context, b db.MirrorBlob, reason string, rep *Report) bool {
	if err := j.DB.DeleteMirrorBlob(ctx, b.Upstream, b.Digest); err != nil {
		slog.Error("mirror: delete blob row failed", "upstream", b.Upstream, "digest", b.Digest, "err", err)
		return false
	}
	if _, err := retention.DeleteBlobIfUnreferenced(ctx, j.DB, j.Store, b.StorageKey, true); err != nil {
		slog.Error("mirror: delete blob failed", "key", b.StorageKey, "err", err)
	}
	rep.FreedBytes += b.Size
	slog.Info("mirror blob deleted", "upstream", b.Upstream, "digest", b.Digest, "bytes", b.Size, "reason", reason)
	return true
}
