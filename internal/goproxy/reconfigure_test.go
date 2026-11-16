package goproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconfigureReplacesTheRunningService(t *testing.T) {
	t.Serial()
	fake := newFakeGitHub(t)
	old := newTestService(t, fake, "", nil)
	old.metrics.record(Event{Outcome: "hit"})

	serviceMu.Lock()
	prevService, prevStop := service, stopReadiness
	service, stopReadiness = old, nil
	serviceMu.Unlock()
	t.Cleanup(func() {
		serviceMu.Lock()
		if stopReadiness != nil {
			stopReadiness()
		}
		service, stopReadiness = prevService, prevStop
		serviceMu.Unlock()
	})

	reconfigure(old.cfg)
	assert.Same(t, old, Current(), "an unchanged configuration keeps the Service")

	reconfigure(Config{Upstream: "https://mirror.example", ReadinessModule: "example.com/team/mod"})
	s := Current()
	require.NotSame(t, old, s)
	assert.Equal(t, "https://mirror.example", s.Config().Upstream)
	assert.Equal(t, "example.com/team/mod", s.Config().ReadinessModule)
	assert.True(t, s.upstream.enabled(), "the new upstream is in use")
	assert.False(t, old.upstream.enabled())
	assert.Same(t, old.metrics, s.metrics, "the counters carry over")
	assert.Same(t, old.health, s.health)
	assert.Same(t, old.github, s.github)
}
