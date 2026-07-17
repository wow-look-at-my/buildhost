package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:generate env CGO_ENABLED=0 go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0 generate -f ../../sqlc.yaml

type DB struct {
	*sql.DB
	q *Queries
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON&_time_format=sqlite")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	d := &DB{DB: sqlDB, q: New(sqlDB)}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}
