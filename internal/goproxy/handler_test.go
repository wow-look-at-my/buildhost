package goproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantMod  string
		wantVer  string
		wantEnd  string
		wantKind Kind
		wantErr  bool
	}{
		{name: "list", path: "/github.com/o/r/@v/list", wantMod: "github.com/o/r", wantEnd: "list"},
		{name: "latest", path: "/github.com/o/r/@latest", wantMod: "github.com/o/r", wantEnd: "latest"},
		{name: "info", path: "/github.com/o/r/@v/v1.2.3.info", wantMod: "github.com/o/r", wantVer: "v1.2.3", wantEnd: "info"},
		{name: "mod", path: "/github.com/o/r/@v/v1.2.3.mod", wantMod: "github.com/o/r", wantVer: "v1.2.3", wantEnd: "mod"},
		{name: "zip", path: "/github.com/o/r/@v/v1.2.3.zip", wantMod: "github.com/o/r", wantVer: "v1.2.3", wantEnd: "zip"},
		{
			name: "nested module", path: "/github.com/o/r/sub/pkg/@v/list",
			wantMod: "github.com/o/r/sub/pkg", wantEnd: "list",
		},
		{
			name: "pseudo-version", path: "/github.com/o/r/@v/v0.0.0-20260721161008-302008ab1248.info",
			wantMod: "github.com/o/r", wantVer: "v0.0.0-20260721161008-302008ab1248", wantEnd: "info",
		},
		{
			// The wire encoding: an uppercase letter travels as "!" + lowercase, so
			name: "case-encoded module path", path: "/github.com/!pazer!o!p/thing/@v/list",
			wantMod: "github.com/PazerOP/thing", wantEnd: "list",
		},
		{
			name: "case-encoded version", path: "/github.com/o/r/@v/v1.0.0-!r!c1.info",
			wantMod: "github.com/o/r", wantVer: "v1.0.0-RC1", wantEnd: "info",
		},
		{name: "not a proxy path", path: "/github.com/o/r", wantErr: true, wantKind: KindInvalidRequest},
		{name: "unknown endpoint", path: "/github.com/o/r/@v/v1.2.3.tar", wantErr: true, wantKind: KindInvalidRequest},
		{name: "bad case encoding", path: "/github.com/O/r/@v/list", wantErr: true, wantKind: KindInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRequest(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				var e *Error
				require.ErrorAs(t, err, &e)
				assert.Equal(t, tt.wantKind, e.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMod, got.Module)
			assert.Equal(t, tt.wantVer, got.Version)
			assert.Equal(t, tt.wantEnd, got.Endpoint)
		})
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	for _, v := range []string{"v1.2.3", "v1.0.0-RC1", "v0.0.0-20260721161008-302008ab1248", "v2.0.0+incompatible"} {
		got, err := unescapeVersion(escapeVersion(v))
		require.NoError(t, err, v)
		assert.Equal(t, v, got)
	}
}

func TestMetricsRingKeepsNewestFirst(t *testing.T) {
	m := newMetrics()
	for i := range recentEvents + 10 {
		m.record(Event{Module: "m", Version: versionFor(i), Outcome: "hit"})
	}
	_, _, _, _, _, recent := m.snapshot()

	require.Len(t, recent, recentEvents)
	assert.Equal(t, versionFor(recentEvents+9), recent[0].Version, "newest event must come first")
	assert.Equal(t, versionFor(10), recent[len(recent)-1].Version)
}

func TestMetricsCountsByOutcome(t *testing.T) {
	m := newMetrics()
	m.record(Event{Outcome: "hit"})
	m.record(Event{Outcome: "fetch"})
	m.record(Event{Outcome: "fetch"})
	m.record(Event{Outcome: "error", Detail: "unauthorized"})

	hits, misses, fetches, _, errs, _ := m.snapshot()
	assert.EqualValues(t, 1, hits)
	assert.EqualValues(t, 2, misses)
	assert.EqualValues(t, 2, fetches)
	assert.EqualValues(t, 1, errs["unauthorized"])
}

func versionFor(i int) string {
	return "v0.0." + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
