package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimRunLock(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	key := RunLockKey{RepoID: "222", RunID: "4242", RunAttempt: "1", Name: "github.com/wow-look-at-my/alpha@HEAD"}
	now := time.Now()

	_, found, err := d.GetRunLock(ctx, key)
	require.NoError(t, err)
	assert.False(t, found)

	value, created, err := d.ClaimRunLock(ctx, key, "v0.0.0-a", now)
	require.NoError(t, err)
	assert.Equal(t, "v0.0.0-a", value)
	assert.True(t, created)

	value, created, err = d.ClaimRunLock(ctx, key, "v0.0.0-b", now)
	require.NoError(t, err)
	assert.Equal(t, "v0.0.0-a", value, "the first claim must win")
	assert.False(t, created)
}

func TestClaimRunLock_ExpiresOldLocks(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()
	old := RunLockKey{RepoID: "222", RunID: "1", RunAttempt: "1", Name: "a@HEAD"}
	_, _, err := d.ClaimRunLock(ctx, old, "v0.0.0-a", time.Now())
	require.NoError(t, err)

	// A claim made after the lifetime has passed removes the lock.
	later := time.Now().Add(RunLockLifetime + time.Hour)
	_, _, err = d.ClaimRunLock(ctx, RunLockKey{RepoID: "222", RunID: "2", RunAttempt: "1", Name: "a@HEAD"}, "v0.0.0-b", later)
	require.NoError(t, err)
	_, found, err := d.GetRunLock(ctx, old)
	require.NoError(t, err)
	assert.False(t, found, "a lock older than RunLockLifetime must be deleted")
}
