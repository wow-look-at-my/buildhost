// Open creates the SQLite database: one writer connection and a pool of readers.

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
	// readDB serves read-only queries a CI job waits on.
	readDB *sql.DB
	rq     *Queries
}

// readIdleConns is how many reader connections stay open between requests.
const readIdleConns = 16

func Open(path string) (*DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_time_format=sqlite"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	d := &DB{DB: sqlDB, q: New(sqlDB)}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	readDB.SetMaxOpenConns(0)
	readDB.SetMaxIdleConns(readIdleConns)
	d.readDB, d.rq = readDB, New(readDB)
	return d, nil
}

// Close closes the writer and the read pool.
func (d *DB) Close() error {
	err := d.DB.Close()
	if d.readDB != nil {
		if rerr := d.readDB.Close(); err == nil {
			err = rerr
		}
	}
	return err
}
