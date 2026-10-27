package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The driver reads only _pragma parameters, so this proves the settings are in force on both pools.
func TestConnectionsRunInWALWithABusyTimeout(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	for name, pool := range map[string]*sql.DB{"writer": d.DB, "reader": d.readDB} {
		var mode string
		require.NoError(t, pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode), name)
		require.Equal(t, "wal", mode, name)
		var timeout int
		require.NoError(t, pool.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout), name)
		require.Equal(t, 5000, timeout, name)
	}
}

// Every concurrent reader gets its own connection, however many hold one at once.
func TestEachConcurrentReaderGetsItsOwnConnection(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const readers = 40
	for i := range readers {
		conn, err := d.readDB.Conn(ctx)
		require.NoError(t, err, "reader %d waited for a free connection", i)
		t.Cleanup(func() { conn.Close() })
	}
}

// A cache lookup must answer while a write transaction holds the writer connection.
func TestCacheLookupAnswersWhileAWriteTransactionIsOpen(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	scope := CacheScope{RepoID: "1", Ref: "refs/heads/master"}

	stored, err := d.PutCache(ctx, scope, "deps-v1", "owner/repo", RunBlob{StorageKey: "sha", Size: 3})
	require.NoError(t, err)
	require.True(t, stored)

	tx, err := d.DB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { tx.Rollback() })
	_, err = tx.ExecContext(ctx, "DELETE FROM cache_entries WHERE key = 'nothing'")
	require.NoError(t, err)

	lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	match, found, err := d.FindCache(lookup, []CacheScope{scope}, "deps-v1", nil)
	require.NoError(t, err, "the lookup waited for the writer connection")
	require.True(t, found)
	require.Equal(t, "sha", match.StorageKey)

	got, found, err := d.GetCache(lookup, scope, "deps-v1")
	require.NoError(t, err, "the download read waited for the writer connection")
	require.True(t, found)
	require.Equal(t, int64(3), got.Size)
}
