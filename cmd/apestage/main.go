// apestage writes the ELF an APE's own trampoline would have staged, so a
// container can exec the binary directly.
//
// The trampoline copies itself to a hardcoded path under /tmp and overwrites the
// copy's header. It reads no TMPDIR, so a container whose /tmp is the usual
// noexec tmpfs dies there against a path nobody chose. Doing the work here, at
// image build time, takes /tmp out of the picture.
//
// Usage: apestage <ape> <out> <arch>
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/repackage"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: apestage <ape> <out> <arch>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2], db.Arch(os.Args[3])); err != nil {
		fmt.Fprintln(os.Stderr, "apestage:", err)
		os.Exit(1)
	}
}

func run(in, out string, arch db.Arch) error {
	src, err := os.Open(in)
	if err != nil {
		return err
	}
	defer src.Close()

	staged, err := repackage.StageAPE(src, arch)
	if err != nil {
		return fmt.Errorf("stage %s for %s: %w", in, arch, err)
	}
	dst, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, staged); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
