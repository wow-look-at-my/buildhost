package db

import (
	"context"
	"database/sql"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	_ "modernc.org/sqlite"
)

var dbTracer = otel.Tracer("buildhost.db")

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

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, span := dbTracer.Start(ctx, "db.exec",
		trace.WithAttributes(
			attribute.String("db.system", "sqlite"),
			attribute.String("db.statement", truncateQuery(query)),
		),
	)
	defer span.End()
	result, err := d.DB.ExecContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return result, err
}

func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	ctx, span := dbTracer.Start(ctx, "db.query",
		trace.WithAttributes(
			attribute.String("db.system", "sqlite"),
			attribute.String("db.statement", truncateQuery(query)),
		),
	)
	defer span.End()
	rows, err := d.DB.QueryContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return rows, err
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	_, span := dbTracer.Start(ctx, "db.query_row",
		trace.WithAttributes(
			attribute.String("db.system", "sqlite"),
			attribute.String("db.statement", truncateQuery(query)),
		),
	)
	defer span.End()
	return d.DB.QueryRowContext(ctx, query, args...)
}

func truncateQuery(q string) string {
	if len(q) <= 200 {
		return q
	}
	return q[:200]
}
