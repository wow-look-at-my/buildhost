package db

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Evicting more releases than one batch holds frees each blob once, and counts a blob shared across batches once.
func TestEvictReleases_SpansBatches(t *testing.T) {
	t.Serial()
	old := evictBatchSize
	evictBatchSize = 2
	t.Cleanup(func() { evictBatchSize = old })

	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")

	ids := make([]int64, 0, 5)
	for i, key := range []string{"shared", "u2", "u3", "shared", "u5"} {
		r := retRelease(t, d, p.ID, "v"+string(rune('1'+i)), int64(i+1), "main")
		retArtifact(t, d, r.ID, key, 10)
		ids = append(ids, r.ID)
	}

	freed, candidates, err := d.EvictReleases(ctx, ids, true)
	require.NoError(t, err)
	assert.Equal(t, 4, candidates, "a blob shared across batches is one candidate")
	assert.Equal(t, []string{"shared", "u2", "u3", "u5"}, keysOf(freed), "the shared blob is freed once, by the batch that drops its last reference")

	for i := range ids {
		_, err = d.GetRelease(ctx, p.ID, "v"+string(rune('1'+i)))
		assert.ErrorIs(t, err, ErrNotFound)
	}
}

// A dry run over more releases than one batch holds still reports the whole total and changes nothing.
func TestEvictReleases_DryRunSpansBatches(t *testing.T) {
	t.Serial()
	old := evictBatchSize
	evictBatchSize = 1
	t.Cleanup(func() { evictBatchSize = old })

	d := openTestDB(t)
	ctx := context.Background()
	p := retProject(t, d, "proj")
	r1 := retRelease(t, d, p.ID, "v1", 1, "main")
	r2 := retRelease(t, d, p.ID, "v2", 2, "main")
	retArtifact(t, d, r1.ID, "a", 10)
	retArtifact(t, d, r2.ID, "b", 10)

	freed, candidates, err := d.EvictReleases(ctx, []int64{r1.ID, r2.ID}, false)
	require.NoError(t, err)
	assert.Equal(t, 2, candidates)
	assert.Equal(t, []string{"a", "b"}, keysOf(freed))
	_, err = d.GetRelease(ctx, p.ID, "v1")
	assert.NoError(t, err)
	_, err = d.GetRelease(ctx, p.ID, "v2")
	assert.NoError(t, err)
}
