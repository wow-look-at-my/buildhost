package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON&_time_format=sqlite")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// WAL mode supports concurrent readers with a single writer.
	// Allow enough connections for parallel read-heavy request handling.
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(4)

	d := &DB{DB: sqlDB}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}
