package repackage

import (
	"bytes"
	"debug/elf"
	"fmt"
	"io"
	"regexp"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// apeHeadSize bounds the prologue read, which holds the trampoline.
const apeHeadSize = 16 << 10

// apeELFHeaderSize is the ELF64 header the trampoline writes over the prologue.
const apeELFHeaderSize = 64

// apeMachine is the ELF e_machine value a patched header must carry for arch.
var apeMachine = map[db.Arch]elf.Machine{
	db.ArchAMD64: elf.EM_X86_64,
	db.ArchARM64: elf.EM_AARCH64,
}

// apePrintfELF matches the trampoline's write of an architecture's ELF header.
var apePrintfELF = regexp.MustCompile(`printf '((?:[^'\\]|\\.)*)'\s*>&7`)

// apeELFHeader returns the ELF header the APE's own trampoline would write for
// arch. An APE opens with a shell script rather than ELF magic, and the
// trampoline overwrites exactly that much of its copy to make it loadable.
// Reading the header out of the binary keeps this tied to the file in hand.
// head must be the start of the APE.
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
		// e_machine is a little-endian uint16 in the ELF64 header.
		if elf.Machine(uint16(hdr[18])|uint16(hdr[19])<<8) == want {
			return hdr, nil
		}
	}
	if found == 0 {
		return nil, fmt.Errorf("the APE prologue holds no ELF header: it is not a trampoline this version understands")
	}
	return nil, fmt.Errorf("the APE prologue holds %d ELF headers and none is %s", found, want)
}

// shellPrintfBytes decodes the escapes in a single-quoted printf argument. The
// trampoline spells each non-printable header byte as an octal escape.
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

// apeAsELF turns the APE stream r into the ELF the kernel loads directly: the
// same file, with the header the trampoline would have written over its shell
// prologue. The length is unchanged, so a known artifact size still holds.
//
// Staging here is what keeps /tmp out of the image. The trampoline stages its
// copy under a hardcoded /tmp path that no environment variable moves, so a
// container whose /tmp is the usual noexec tmpfs dies on a path nobody chose.
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
