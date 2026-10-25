package recompress

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The window table is bounded: a public registry sees many addresses, and a
// table that only grows is a leak.
func TestLimiterPurgesExpiredWindows(t *testing.T) {
	t.Serial()
	now := time.Now()
	l := NewLimiter(LimiterConfig{PerIP: 1, Window: time.Minute, LoadCeiling: 1}, func() float64 { return 0 })
	l.now = func() time.Time { return now }
	for i := 0; i < maxTrackedIPs+50; i++ {
		l.Allow(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	require.Greater(t, len(l.seen), maxTrackedIPs, "the table grew past its bound before a purge")

	l.now = func() time.Time { return now.Add(2 * time.Minute) }
	l.Allow("203.0.113.9")
	assert.LessOrEqual(t, len(l.seen), 2, "expired windows are dropped once the table is over its bound")
}

func TestServiceWiresTheCacheAndLimiter(t *testing.T) {
	t.Serial()
	dir := filepath.Join(t.TempDir(), "cache")
	svc := NewService(Config{
		CacheLimit: 1 << 20, CacheEntries: 4, CacheTTL: time.Hour,
		PerIP: 2, Window: time.Minute, LoadCeiling: 1,
	}, dir)
	t.Cleanup(svc.Close)
	require.NotNil(t, svc.Cache)
	require.NotNil(t, svc.Limiter)
	assert.Equal(t, 4, svc.Cache.entries)
	assert.True(t, svc.Limiter.Allow("203.0.113.9"))
}

func TestSharedServiceRoundTrip(t *testing.T) {
	t.Serial()
	previous := Get()
	t.Cleanup(func() { Set(previous) })

	svc := NewService(Config{CacheLimit: 1 << 20, CacheEntries: 4, CacheTTL: time.Hour}, t.TempDir())
	Set(svc)
	assert.Same(t, svc, Get())
}
