package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
