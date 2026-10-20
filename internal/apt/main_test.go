package apt

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests in this package build a Signer, and each generates its own key.
func TestMain(m *testing.M) {
	rsaBits = 1024
	os.Exit(m.Run())
}

// The size above is a test convenience and must never become the shipped one.
// TestMain must stay the only thing that lowers it -- delete it and the suite
// quietly goes back to seconds per key.
func TestSigningKeySizeIsLoweredForTestsOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 4096, productionRSABits, "buildhost ships 4096-bit signing keys")
	assert.Less(t, rsaBits, productionRSABits, "TestMain did not lower the test key size")
}
