package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A test binary is not stamped with VCS settings by the toolchain, so Commit

func TestGetIsStable(t *testing.T) {
	require.Equal(t, Get(), Get())
}

func TestCommitNeverEmpty(t *testing.T) {
	require.NotEqual(t, "", Commit())
}

func TestVersionNeverEmpty(t *testing.T) {
	require.NotEqual(t, "", Version())
}
