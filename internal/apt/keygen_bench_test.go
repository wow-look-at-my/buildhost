// Evidence for the key size TestMain picks: what each RSA size costs to
// generate, and what a test actually pays for a Signer.
package apt

import (
	"crypto"
	"fmt"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/stretchr/testify/require"
)

func BenchmarkKeygenBySize(b *testing.B) {
	for _, bits := range []int{1024, 2048, 3072, 4096} {
		b.Run(fmt.Sprint(bits), func(b *testing.B) {
			for b.Loop() {
				_, err := openpgp.NewEntity("Buildhost", "APT Release signing", "apt@buildhost.local", &packet.Config{
					RSABits:     bits,
					DefaultHash: crypto.SHA256,
				})
				require.Nil(b, err)
			}
		})
	}
}

// The whole cost a test pays: generate, serialize, write, then load back.
func BenchmarkNewSignerAsTestsUseIt(b *testing.B) {
	for b.Loop() {
		s := NewSigner(b.TempDir())
		require.True(b, s.Available())
	}
}
