// Package runfiles deletes the files a CI run handed between its jobs, once
// the run no longer needs them. A run's files go Keep after GitHub reports the
// run complete, or Keep after their last use when GitHub cannot report the
// run. They go sooner, oldest-ended first, when free disk falls below
// MinFreePercent. A live run's files never go early: a job of that run still
// waits for them.
package runfiles

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

// MaxRunLifetime bounds a run whose end GitHub never reports. It is the longest a workflow run can last.
const MaxRunLifetime = db.RunLockLifetime

// CheckInterval is how often the janitor runs, and how often it asks GitHub about each live run.
const CheckInterval = 5 * time.Minute

type Janitor struct {
	DB             *db.DB
	Store          storage.Storage
	DataDir        string
	Keep           time.Duration
	MinFreePercent float64

	// RunEnded asks GitHub whether a run is complete. See auth.GitHubRunEnded.
	RunEnded func(ctx context.Context, repoPath, runID string) (time.Time, bool, error)
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
	Checked         int       `json:"checked"`
	CheckFailures   int       `json:"check_failures"`
	LastCheckError  string    `json:"last_check_error,omitempty"`
	Ended           int       `json:"ended"`
	Deleted         int       `json:"deleted"`
	PressureDeleted int       `json:"pressure_deleted"`
	FreedBytes      int64     `json:"freed_bytes"`
	Error           string    `json:"error,omitempty"`
}

var lastReport atomic.Pointer[Report]

// LastReport returns the most recent pass, or nil before the first one.
func LastReport() *Report { return lastReport.Load() }

// Start runs Sweep every CheckInterval until ctx is done.
func (j *Janitor) Start(ctx context.Context) {
	slog.Info("run files janitor enabled", "keep", j.Keep, "min_free_percent", j.MinFreePercent)
	go func() {
		t := time.NewTicker(CheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if j.Busy != nil && j.Busy() {
					slog.Info("run files: deferring sweep, writes in flight")
					lastReport.Store(&Report{At: time.Now(), Deferred: true})
					continue
				}
				if err := j.Sweep(ctx, time.Now()); err != nil {
					slog.Error("run files sweep failed", "err", err)
				}
			}
		}
	}()
}

// Sweep reads the status of the live runs, then deletes what no run needs.
// It records what it did for LastReport.
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
	unknown, err := j.checkRuns(ctx, now, rep)
	if err != nil {
		return err
	}
	runs, err := j.DB.RunStorageRuns(ctx)
	if err != nil {
		return err
	}
	var ended []db.ListRunStorageRunsRow
	for _, run := range runs {
		switch {
		case run.EndedAt != nil && now.Sub(*run.EndedAt) >= j.Keep:
			j.deleteRun(ctx, run, "ended "+now.Sub(*run.EndedAt).Round(time.Minute).String()+" ago", rep)
		// Without this, a server with no GitHub credential keeps every run's files for MaxRunLifetime.
		case unknown[db.RunKey{RepoID: run.RepoID, RunID: run.RunID}] && now.Sub(run.UsedAt) >= j.Keep:
			j.deleteRun(ctx, run, "GitHub cannot report its status and nothing used it for "+now.Sub(run.UsedAt).Round(time.Minute).String(), rep)
		case now.Sub(run.CreatedAt) >= MaxRunLifetime:
			j.deleteRun(ctx, run, "older than the longest a run can last, and GitHub never reported its end", rep)
		case run.EndedAt != nil:
			ended = append(ended, run)
		}
	}
	return j.relievePressure(ctx, ended, runs, rep)
}

// checkRuns returns the runs whose status GitHub did not answer.
func (j *Janitor) checkRuns(ctx context.Context, now time.Time, rep *Report) (map[db.RunKey]bool, error) {
	runs, err := j.DB.RunsToCheck(ctx, now.Add(-CheckInterval))
	if err != nil {
		return nil, err
	}
	unknown := map[db.RunKey]bool{}
	for _, run := range runs {
		key := db.RunKey{RepoID: run.RepoID, RunID: run.RunID}
		rep.Checked++
		endedAt, done, err := j.RunEnded(ctx, run.RepoPath, run.RunID)
		if err != nil {
			rep.CheckFailures++
			rep.LastCheckError = err.Error()
			unknown[key] = true
			slog.Warn("run files: cannot read the run's status from GitHub; its files stay until nothing uses them for the keep period", "repo", run.RepoPath, "run_id", run.RunID, "keep", j.Keep, "err", err)
			continue
		}
		var mark *time.Time
		if done {
			mark = &endedAt
			rep.Ended++
		}
		if err := j.DB.MarkRunChecked(ctx, key, mark); err != nil {
			return nil, err
		}
	}
	return unknown, nil
}

// relievePressure deletes ended runs, oldest-ended first, until free disk is
// back above MinFreePercent. ended is in that order already.
func (j *Janitor) relievePressure(ctx context.Context, ended, all []db.ListRunStorageRunsRow, rep *Report) error {
	if j.MinFreePercent <= 0 {
		return nil
	}
	low := func() (bool, error) {
		du, err := j.Disk(j.DataDir)
		if err != nil {
			return false, err
		}
		if du.Total == 0 {
			return false, nil
		}
		return float64(du.Free)*100/float64(du.Total) < j.MinFreePercent, nil
	}
	for _, run := range ended {
		isLow, err := low()
		if err != nil || !isLow {
			return err
		}
		if j.deleteRun(ctx, run, "free disk is below "+formatPercent(j.MinFreePercent), rep) {
			rep.PressureDeleted++
		}
	}
	isLow, err := low()
	if err != nil || !isLow {
		return err
	}
	var live int64
	for _, run := range all {
		if run.EndedAt == nil {
			live += run.Bytes
		}
	}
	slog.Error("run files: free disk is below the floor and no ended run is left to delete", "min_free_percent", j.MinFreePercent, "live_run_bytes", live)
	return nil
}

func (j *Janitor) deleteRun(ctx context.Context, run db.ListRunStorageRunsRow, reason string, rep *Report) bool {
	keys, err := j.DB.DeleteRun(ctx, db.RunKey{RepoID: run.RepoID, RunID: run.RunID})
	if err != nil {
		slog.Error("run files: delete run failed", "repo", run.RepoPath, "run_id", run.RunID, "err", err)
		return false
	}
	for _, key := range keys {
		if _, err := retention.DeleteBlobIfUnreferenced(ctx, j.DB, j.Store, key, true); err != nil {
			slog.Error("run files: delete blob failed", "key", key, "err", err)
		}
	}
	rep.Deleted++
	rep.FreedBytes += run.Bytes
	slog.Info("run files deleted", "repo", run.RepoPath, "run_id", run.RunID, "bytes", run.Bytes, "reason", reason)
	return true
}

func formatPercent(p float64) string {
	return strconv.FormatFloat(p, 'f', -1, 64) + "%"
}
