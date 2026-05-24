package repackage

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/require"
)

func BenchmarkRepackage(b *testing.B) {
	bin, err := os.ReadFile("/usr/local/bin/go-toolchain")
	if err != nil {
		b.Skip("go-toolchain binary not found")
	}

	input := Input{
		Project:	model.Project{Name: "go-toolchain", Description: "Go build toolchain"},
		Release:	model.Release{Version: "1.0.0", VersionNum: 1},
		Artifact:	model.Artifact{OS: model.OSLinux, Arch: model.ArchAMD64, Kind: model.KindBinary},
		Data:		bin,
		BaseURL:	"https://example.com",
	}

	repackagers := []Repackager{
		&TarGZ{},
		&TarXZ{},
		&TarZST{},
		&Zip{},
		&Deb{},
		&NPM{},
	}

	for _, rp := range repackagers {
		b.Run(string(rp.Format()), func(b *testing.B) {
			b.SetBytes(int64(len(bin)))
			b.ReportAllocs()
			for b.Loop() {
				out, err := rp.Repackage(context.Background(), input)
				require.Nil(b, err)

				io.Copy(io.Discard, out.Reader)
			}
		})
	}
}
