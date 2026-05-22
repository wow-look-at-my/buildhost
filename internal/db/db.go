package db

import (
	"database/sql"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db/dbgen"

	_ "modernc.org/sqlite"
)

//go:generate sqlc generate -f ../../sqlc.yaml

type DB struct {
	*sql.DB
	q *dbgen.Queries
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON&_time_format=sqlite")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	d := &DB{DB: sqlDB, q: dbgen.New(sqlDB)}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}
