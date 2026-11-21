package recompress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// allowN reports how many of n requests from one IP were admitted.
func allowN(l *Limiter, ip string, n int) int {
	allowed := 0
	for i := 0; i < n; i++ {
		if l.Allow(ip) {
			allowed++
		}
	}
	return allowed
}

func TestLimiterAllowanceAtRest(t *testing.T) {
	t.Serial()
	l := NewLimiter(LimiterConfig{PerIP: 3, Window: time.Minute, LoadCeiling: 1}, func() float64 { return 0 })
	assert.Equal(t, 3, allowN(l, "203.0.113.9", 10))
}

// The allowance shrinks as load rises and reaches zero at the ceiling, where
// no new recompression starts.
func TestLimiterScalesWithLoad(t *testing.T) {
	t.Serial()
	tests := []struct {
		name    string
		perIP   int
		ceiling float64
		load    float64
		want    int
	}{
		{"idle", 8, 1, 0, 8},
		{"half loaded", 8, 1, 0.5, 4},
		{"near ceiling", 8, 1, 0.9, 1},
		{"at ceiling", 8, 1, 1, 0},
		{"over ceiling", 8, 1, 4, 0},
		{"one per ip survives mild load", 1, 1, 0.3, 1},
		{"one per ip at heavy load admits its first", 1, 1, 0.9, 1},
		{"one per ip at the ceiling", 1, 1, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewLimiter(LimiterConfig{PerIP: tt.perIP, Window: time.Minute, LoadCeiling: tt.ceiling},
				func() float64 { return tt.load })
			assert.Equal(t, tt.want, allowN(l, "203.0.113.9", tt.perIP+5))
		})
	}
}

func TestLimiterIsPerIP(t *testing.T) {
	t.Serial()
	l := NewLimiter(LimiterConfig{PerIP: 1, Window: time.Minute, LoadCeiling: 1}, func() float64 { return 0 })
	assert.True(t, l.Allow("203.0.113.9"))
	assert.False(t, l.Allow("203.0.113.9"))
	assert.True(t, l.Allow("203.0.113.10"), "another client has its own allowance")
}

func TestLimiterWindowResets(t *testing.T) {
	t.Serial()
	now := time.Now()
	l := NewLimiter(LimiterConfig{PerIP: 1, Window: time.Minute, LoadCeiling: 1}, func() float64 { return 0 })
	l.now = func() time.Time { return now }
	assert.True(t, l.Allow("203.0.113.9"))
	assert.False(t, l.Allow("203.0.113.9"))
	l.now = func() time.Time { return now.Add(2 * time.Minute) }
	assert.True(t, l.Allow("203.0.113.9"))
}

// A zero allowance is the operator switching on-the-fly recompression off,
// and a ceiling of zero means system load is not consulted.
func TestLimiterZeroSettings(t *testing.T) {
	t.Serial()
	off := NewLimiter(LimiterConfig{PerIP: 0, Window: time.Minute, LoadCeiling: 1}, func() float64 { return 0 })
	assert.False(t, off.Allow("203.0.113.9"))

	noCeiling := NewLimiter(LimiterConfig{PerIP: 2, Window: time.Minute, LoadCeiling: 0}, func() float64 { return 400 })
	assert.Equal(t, 2, allowN(noCeiling, "203.0.113.9", 5))
}

func TestSamplerTakesAnImmediateSample(t *testing.T) {
	t.Serial()
	s := NewSampler(time.Millisecond, func() float64 { return 2.5 })
	s.Start()
	defer s.Stop()
	assert.Equal(t, 2.5, s.Load())
}
