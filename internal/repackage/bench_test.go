package repackage

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func benchInput(b *testing.B, size int) Input {
	b.Helper()
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		b.Fatal(err)
	}
	return Input{
		Project:  model.Project{Name: "bench", Description: "benchmark project"},
		Release:  model.Release{Version: "1.0.0", VersionNum: 1},
		Artifact: model.Artifact{OS: model.OSLinux, Arch: model.ArchAMD64, Kind: model.KindBinary},
		Data:     data,
		BaseURL:  "https://example.com",
	}
}

func BenchmarkRepackage(b *testing.B) {
	sizes := []int{1 << 20, 5 << 20, 20 << 20}
	repackagers := []Repackager{
		&TarGZ{},
		&TarXZ{},
		&TarZST{},
		&Zip{},
		&Deb{},
		&NPM{},
	}

	for _, size := range sizes {
		label := fmt.Sprintf("%dMB", size>>20)
		input := benchInput(b, size)
		for _, rp := range repackagers {
			b.Run(label+"/"+string(rp.Format()), func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					out, err := rp.Repackage(context.Background(), input)
					if err != nil {
						b.Fatal(err)
					}
					io.Copy(io.Discard, out.Reader)
				}
			})
		}
	}
}
