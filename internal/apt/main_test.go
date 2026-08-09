package apt

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Nine tests in this package build a Signer, and each generates its own key --
// this is the package that tests key generation, so none of them may share one.
// Measured on 4 cores (BenchmarkKeygenBySize): 4096 bits costs 1-3s per key and
// varies wildly with entropy, 3072 is ~370ms, 2048 is ~120ms, and 1024 is
// ~24ms, so only 1024 keeps a generation comfortably under 100ms. Every test
// still generates, saves, loads, signs and verifies with a real key; it is just
// a key nobody ships.
func TestMain(m *testing.M) {
	rsaBits = 1024
	os.Exit(m.Run())
}

// The size above is a test convenience and must never become the shipped one,
// and TestMain must stay the only thing that lowers it -- delete it and the
// suite quietly goes back to seconds per key.
func TestSigningKeySizeIsLoweredForTestsOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 4096, productionRSABits, "buildhost ships 4096-bit signing keys")
	assert.Less(t, rsaBits, productionRSABits, "TestMain did not lower the test key size")
}
