// Sampler caches the host load so a request never waits on the kernel for it.

package recompress

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

func HostLoad() float64 {
	cpus := runtime.NumCPU()
	if cpus < 1 {
		cpus = 1
	}
	return hostLoad1() / float64(cpus)
}

// Sampler caches the host load so a request never waits on the kernel for it.
// The decision function reads the last sample.
type Sampler struct {
	every time.Duration
	read  func() float64

	mu    sync.RWMutex
	value float64

	once sync.Once
	stop chan struct{}
}

// NewSampler builds a sampler that refreshes every interval. A non-positive
// interval means a few seconds.
func NewSampler(every time.Duration, read func() float64) *Sampler {
	if every <= 0 {
		every = 5 * time.Second
	}
	if read == nil {
		read = HostLoad
	}
	return &Sampler{every: every, read: read, stop: make(chan struct{})}
}

// Load is the most recent sample.
func (s *Sampler) Load() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Start takes an immediate sample and then refreshes in the background until
// Stop is called.
func (s *Sampler) Start() {
	s.once.Do(func() {
		s.sample()
		go s.run()
	})
}

// Stop ends the refresh loop.
func (s *Sampler) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}

func (s *Sampler) sample() {
	v := s.read()
	s.mu.Lock()
	s.value = v
	s.mu.Unlock()
}

func (s *Sampler) run() {
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.sample()
		}
	}
}

// readProcLoadavg reads Linux's /proc/loadavg.
func readProcLoadavg() (float64, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	field, _, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	v, err := strconv.ParseFloat(field, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// sysctlLoadavg asks the host's own sysctl program for vm.loadavg. A
// Cosmopolitan build reports GOOS "cosmo" on every platform and satisfies the
// linux build tag. A binary running on macOS needs this rather than procfs.
func sysctlLoadavg() float64 {
	out, err := exec.Command("/usr/sbin/sysctl", "-n", "vm.loadavg").Output()
	if err != nil {
		out, err = exec.Command("sysctl", "-n", "vm.loadavg").Output()
	}
	if err != nil {
		return 0
	}
	return parseLoadavgText(string(out))
}

// parseLoadavgText reads the kernel's "{ 5.44 8.50 5.88 }" form.
func parseLoadavgText(out string) float64 {
	fields := strings.Fields(strings.Trim(out, "{} \t\r\n"))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}
