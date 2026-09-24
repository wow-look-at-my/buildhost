package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Backup writes a consistent copy of the database at src to dst. It opens src
// read-only and skips migrations, so it never changes the source. A running
// server can keep writing while it runs. It fails if dst already holds data.
func Backup(ctx context.Context, src, dst string) error {
	sqlDB, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open database %s: %w", src, err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)

	if _, err := sqlDB.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
		return fmt.Errorf("back up %s to %s: %w", src, dst, err)
	}
	return nil
}
