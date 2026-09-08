package repackage

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"regexp"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apeHeadSize bounds the prologue this package reads to find the ELF header. The
// trampoline sits in the first few kilobytes, ahead of the payload.
const apeHeadSize = 16 << 10

// apeELFHeaderSize is the ELF64 header the trampoline writes over the prologue.
const apeELFHeaderSize = 64

// apeMachine is the ELF e_machine value a patched header must carry for arch.
var apeMachine = map[db.Arch]elf.Machine{
	db.ArchAMD64: elf.EM_X86_64,
	db.ArchARM64: elf.EM_AARCH64,
}

// apePrintfELF matches one `printf '...' >&7` call in the trampoline. Each such
// call writes the ELF header for one architecture over the copy the trampoline
// just made. The payload is a single-quoted shell word, so the only escape
// inside it is a backslash sequence.
var apePrintfELF = regexp.MustCompile(`printf '((?:[^'\\]|\\.)*)'\s*>&7`)

// apeELFHeader returns the ELF header the APE's own trampoline would write for
// arch. An APE is not an ELF file: its first bytes are a shell script, and the
// trampoline copies the file somewhere writable and executable and overwrites
// exactly those bytes to turn the copy into a real ELF.
//
// Reading the header out of the binary rather than hardcoding one keeps this
// tied to the file in hand. head must be the start of the APE.
func apeELFHeader(head []byte, arch db.Arch) ([]byte, error) {
	want, ok := apeMachine[arch]
	if !ok {
		return nil, fmt.Errorf("no ELF machine is known for %s", arch)
	}
	var found int
	for _, m := range apePrintfELF.FindAllSubmatch(head, -1) {
		hdr := shellPrintfBytes(m[1])
		if len(hdr) != apeELFHeaderSize || !bytes.HasPrefix(hdr, []byte("\x7fELF")) {
			continue
		}
		found++
		// e_machine is a little-endian uint16 at offset 18 of an ELF64 header.
		if elf.Machine(uint16(hdr[18])|uint16(hdr[19])<<8) == want {
			return hdr, nil
		}
	}
	if found == 0 {
		return nil, fmt.Errorf("the APE prologue holds no ELF header: it is not a trampoline this version understands")
	}
	return nil, fmt.Errorf("the APE prologue holds %d ELF headers and none is %s", found, want)
}

// shellPrintfBytes decodes the escapes in a single-quoted printf argument. An
// octal escape is a backslash and up to three octal digits, which is how the
// trampoline spells every non-printable byte of the header.
func shellPrintfBytes(s []byte) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			out = append(out, s[i])
			continue
		}
		i++
		if s[i] < '0' || s[i] > '7' {
			out = append(out, s[i])
			continue
		}
		var v byte
		for n := 0; n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7'; n++ {
			v = v<<3 | (s[i] - '0')
			i++
		}
		i--
		out = append(out, v)
	}
	return out
}

// apeAsELF turns the APE stream r into the ELF the kernel can exec directly,
// which is the same file with its shell prologue replaced by the header the
// trampoline would have written. The length does not change, so a caller that
// already knows the artifact size keeps it.
//
// Doing this once, here, is what keeps /tmp out of the image: the trampoline
// stages its copy under a hardcoded /tmp/.ape-run-1-$(id -u) that it picks
// itself and that no environment variable moves, so a container whose /tmp is
// the usual noexec tmpfs dies at exit 126 with a path nobody chose.
func apeAsELF(r io.Reader, arch db.Arch) (io.Reader, error) {
	head := make([]byte, apeHeadSize)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("read the APE prologue: %w", err)
	}
	head = head[:n]
	if len(head) < apeELFHeaderSize {
		return nil, fmt.Errorf("the artifact is %d bytes, too short to be an APE", len(head))
	}
	hdr, err := apeELFHeader(head, arch)
	if err != nil {
		return nil, err
	}
	copy(head, hdr)
	return io.MultiReader(bytes.NewReader(head), r), nil
}
