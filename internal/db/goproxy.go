package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type GoproxyCached struct {
	Version     string
	CommitSHA   string
	CommittedAt time.Time
	GoMod       []byte
	ZipKey      string
	ZipSize     int64
}

func (d *DB) GoproxyModuleID(ctx context.Context, modulePath, source string) (int64, error) {
	id, err := New(d.DB).UpsertGoproxyModule(ctx, UpsertGoproxyModuleParams{
		ModulePath: modulePath,
		Source:     source,
	})
	if err != nil {
		return 0, fmt.Errorf("upsert goproxy module %s: %w", modulePath, err)
	}
	return id, nil
}

// GetGoproxyCached returns a cached version, or ErrNotFound.
func (d *DB) GetGoproxyCached(ctx context.Context, modulePath, version string) (*GoproxyCached, error) {
	row, err := New(d.DB).GetGoproxyVersion(ctx, GetGoproxyVersionParams{
		ModulePath: modulePath,
		Version:    version,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get goproxy version %s@%s: %w", modulePath, version, err)
	}
	c := &GoproxyCached{
		Version:   row.Version,
		CommitSHA: row.CommitSha,
		GoMod:     []byte(row.GoMod),
		ZipKey:    row.ZipStorageKey,
		ZipSize:   row.ZipSize,
	}
	if row.CommittedAt != nil {
		c.CommittedAt = *row.CommittedAt
	}
	return c, nil
}

// PutGoproxyCached records a resolved version. The zip key may be empty: an
// .info or .mod request resolves a version without ever building the zip, and
// the later .zip request fills it in via SetGoproxyZip.
func (d *DB) PutGoproxyCached(ctx context.Context, moduleID int64, c *GoproxyCached) error {
	var committed *time.Time
	if !c.CommittedAt.IsZero() {
		t := c.CommittedAt
		committed = &t
	}
	err := New(d.DB).UpsertGoproxyVersion(ctx, UpsertGoproxyVersionParams{
		ModuleID:      moduleID,
		Version:       c.Version,
		CommitSha:     c.CommitSHA,
		CommittedAt:   committed,
		GoMod:         string(c.GoMod),
		ZipStorageKey: c.ZipKey,
		ZipSize:       c.ZipSize,
	})
	if err != nil {
		return fmt.Errorf("upsert goproxy version %s: %w", c.Version, err)
	}
	return nil
}

func (d *DB) SetGoproxyZip(ctx context.Context, moduleID int64, version, key string, size int64) error {
	err := New(d.DB).SetGoproxyVersionZip(ctx, SetGoproxyVersionZipParams{
		ZipStorageKey: key,
		ZipSize:       size,
		ModuleID:      moduleID,
		Version:       version,
	})
	if err != nil {
		return fmt.Errorf("set goproxy zip %s: %w", version, err)
	}
	return nil
}

// MarkGoproxySuccess and MarkGoproxyFailure keep the last outcome per module, so
// a module that is failing is visible on the dashboard rather than only in a log
func (d *DB) MarkGoproxySuccess(ctx context.Context, moduleID int64) error {
	if err := New(d.DB).MarkGoproxyModuleSuccess(ctx, moduleID); err != nil {
		return fmt.Errorf("mark goproxy success: %w", err)
	}
	return nil
}

func (d *DB) MarkGoproxyFailure(ctx context.Context, moduleID int64, kind, detail string) error {
	err := New(d.DB).MarkGoproxyModuleError(ctx, MarkGoproxyModuleErrorParams{
		LastErrorKind: kind,
		LastError:     detail,
		ID:            moduleID,
	})
	if err != nil {
		return fmt.Errorf("mark goproxy failure: %w", err)
	}
	return nil
}

func (d *DB) ListGoproxyModules(ctx context.Context) ([]ListGoproxyModuleSummariesRow, error) {
	rows, err := New(d.DB).ListGoproxyModuleSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list goproxy modules: %w", err)
	}
	return rows, nil
}

func (d *DB) ListGoproxyVersions(ctx context.Context, modulePath string) ([]ListGoproxyVersionsByModuleRow, error) {
	rows, err := New(d.DB).ListGoproxyVersionsByModule(ctx, modulePath)
	if err != nil {
		return nil, fmt.Errorf("list goproxy versions: %w", err)
	}
	return rows, nil
}

func (d *DB) GoproxyCacheStats(ctx context.Context) (GetGoproxyCacheStatsRow, error) {
	row, err := New(d.DB).GetGoproxyCacheStats(ctx)
	if err != nil {
		return row, fmt.Errorf("goproxy cache stats: %w", err)
	}
	return row, nil
}
