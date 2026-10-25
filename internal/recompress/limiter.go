// maxTrackedIPs bounds the per-IP window table.

package recompress

import (
	"sync"
	"time"
)

// maxTrackedIPs bounds the per-IP window table.
const maxTrackedIPs = 4096

// LimiterConfig is the recompression budget.
type LimiterConfig struct {
	// PerIP is the number of recompressions one IP may start per Window.
	PerIP int
	// Window is how long one allowance lasts.
	Window time.Duration
	// LoadCeiling is the host load average per CPU at which the allowance reaches zero.
	LoadCeiling float64
}

type ipWindow struct {
	start time.Time
	count int
}

// Limiter bounds on-the-fly recompression per client IP, scaled by the host's
// measured load.
type Limiter struct {
	cfg  LimiterConfig
	load func() float64
	now  func() time.Time

	mu   sync.Mutex
	seen map[string]*ipWindow
}

// NewLimiter builds a limiter that reads load on each decision.
func NewLimiter(cfg LimiterConfig, load func() float64) *Limiter {
	if load == nil {
		load = HostLoad
	}
	return &Limiter{cfg: cfg, load: load, now: time.Now, seen: map[string]*ipWindow{}}
}

// Allowance is how many recompressions one IP may start in the current window
// at the current load. It falls linearly with load and is zero at or above the
// ceiling.
func (l *Limiter) Allowance() float64 {
	per := float64(l.cfg.PerIP)
	if per <= 0 {
		return 0
	}
	if l.cfg.LoadCeiling <= 0 {
		return per
	}
	load := l.load()
	if load <= 0 {
		return per
	}
	if load >= l.cfg.LoadCeiling {
		return 0
	}
	return per * (1 - load/l.cfg.LoadCeiling)
}

// Allow reports whether ip may start one more recompression now.
func (l *Limiter) Allow(ip string) bool {
	allowance := l.Allowance()
	if allowance <= 0 {
		return false
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.seen[ip]
	if !ok || l.cfg.Window <= 0 || now.Sub(w.start) >= l.cfg.Window {
		w = &ipWindow{start: now}
		l.seen[ip] = w
		l.purgeLocked(now)
	}
	// Compare before counting this request, so an allowance below one still
	// admits the first request from a client.
	if float64(w.count) >= allowance {
		return false
	}
	w.count++
	return true
}

// purgeLocked drops windows that have expired, once the table is large enough
// to be worth walking.
func (l *Limiter) purgeLocked(now time.Time) {
	if len(l.seen) <= maxTrackedIPs || l.cfg.Window <= 0 {
		return
	}
	for ip, w := range l.seen {
		if now.Sub(w.start) >= l.cfg.Window {
			delete(l.seen, ip)
		}
	}
}
