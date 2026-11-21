// coding is a response transfer coding.

package static

import (
	"io"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
)

// coding is a response transfer coding. The empty value is identity.
type coding string

const (
	codingIdentity coding = ""
	codingZstd     coding = "zstd"
	codingBr       coding = "br"
	codingGzip     coding = "gzip"
)

// Negotiate picks the response coding from Accept-Encoding. zstd comes first
// because the stored stream is already zstd and passing it through costs no
// work; brotli beats gzip on size.
func Negotiate(accept string) coding {
	switch {
	case acceptsCoding(accept, "zstd"):
		return codingZstd
	case acceptsCoding(accept, "br"):
		return codingBr
	case acceptsCoding(accept, "gzip"):
		return codingGzip
	default:
		return codingIdentity
	}
}

// acceptsCoding reports whether Accept-Encoding offers name at a non-zero
// quality.
func acceptsCoding(accept, name string) bool {
	for _, part := range strings.Split(accept, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(token), name) {
			continue
		}
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(p, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
				continue
			}
			if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q == 0 {
				return false
			}
		}
		return true
	}
	return false
}

func acceptsZstd(accept string) bool { return acceptsCoding(accept, "zstd") }

// compress copies src to dst under the chosen coding.
func compress(enc coding, dst io.Writer, src io.Reader) error {
	switch enc {
	case codingBr:
		w := brotli.NewWriterLevel(dst, brotli.DefaultCompression)
		if _, err := io.Copy(w, src); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	case codingGzip:
		w := gzip.NewWriter(dst)
		if _, err := io.Copy(w, src); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	default:
		_, err := io.Copy(dst, src)
		return err
	}
}
