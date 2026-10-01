package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A test binary is not stamped with VCS settings by the toolchain, so Commit

func TestGetIsStable(t *testing.T) {
	t.Serial()
	require.Equal(t, Get(), Get())
}

func TestCommitNeverEmpty(t *testing.T) {
	t.Serial()
	require.NotEqual(t, "", Commit())
}

func TestVersionNeverEmpty(t *testing.T) {
	t.Serial()
	require.NotEqual(t, "", Version())
}
