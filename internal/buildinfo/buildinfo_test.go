package buildinfo

import (
	"testing"

	"github.com/wow-look-at-my/testify/require"
)

// A test binary is not stamped with VCS settings by the toolchain, so Commit
// and Version fall back to their sentinels here. The contract these tests pin
// is that the accessors are stable across calls and never return an empty
// string, which is what /healthz and the version command rely on.

func TestGetIsStable(t *testing.T) {
	require.Equal(t, Get(), Get())
}

func TestCommitNeverEmpty(t *testing.T) {
	require.NotEqual(t, "", Commit())
}

func TestVersionNeverEmpty(t *testing.T) {
	require.NotEqual(t, "", Version())
}
