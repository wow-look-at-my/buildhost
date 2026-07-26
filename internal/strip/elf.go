package strip

// Native ELF stripping: no strip(1), no objcopy(1), no BFD.
//
// Why this exists at all: buildhost's production image is
// gcr.io/distroless/static-debian12, which ships no binutils, so the
// shell-out implementation this replaces was never able to run there. The
// feature silently no-opped in production for weeks -- `X-Debug-Symbols:
// unavailable` on every download -- while the documented design says
// stripping happens on demand at download time. Doing it in-process makes the
// behavior identical everywhere buildhost runs, independent of what happens to
// be installed on the host.
//
// The second reason is safety. strip/objcopy go through BFD, which accepts
// PE/COFF and Mach-O as well as ELF and rewrites those inputs instead of
// failing -- that is how a Cosmopolitan APE artifact (a PE32+ to BFD) was
// served corrupted and non-reproducibly. This implementation parses ELF and
// only ELF; anything else is refused before a byte is written, and the caller
// serves the artifact untouched.
//
// What it does, matching `strip --strip-debug --strip-unneeded` closely enough
// for a download path: drop the non-SHF_ALLOC debug and symbol-table sections,
// keep everything a loader or the Go runtime can reach. Allocated sections are
// never moved -- program headers address them by file offset, so relocating
// one would break execution -- which also means the transformation is a
// truncate-and-rewrite-the-section-table, not a general ELF rewriter.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ErrUnsupportedELF is returned for ELF files this implementation deliberately
// declines to rewrite (32-bit, or an extended section table). Like ErrNotELF it
// means "serve the artifact unstripped".
var ErrUnsupportedELF = errors.New("strip: unsupported ELF variant")

// elfMagic is the 4-byte header every ELF file starts with.
var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

const (
	elfHeaderSize64  = 64
	sectionEntrySize = 64

	// ELF64 header field offsets.
	offPhoff     = 0x20
	offShoff     = 0x28
	offEhsize    = 0x34
	offPhentsize = 0x36
	offPhnum     = 0x38
	offShentsize = 0x3a
	offShnum     = 0x3c
	offShstrndx  = 0x3e

	// ELF64 section header field offsets.
	shName      = 0x00
	shType      = 0x04
	shFlags     = 0x08
	shOffset    = 0x18
	shSize      = 0x20
	shAddralign = 0x30

	shtNobits = 8
	shfAlloc  = 0x2
)

// section is a raw section header plus the decoded fields this package needs.
// The 64 header bytes are carried verbatim so fields we do not understand
// (link, info, entsize, ...) survive the rewrite untouched.
type section struct {
	raw       []byte
	name      string
	typ       uint32
	flags     uint64
	offset    uint64
	size      uint64
	addralign uint64

	keep      bool
	newOffset uint64
}

func (s *section) alloc() bool   { return s.flags&shfAlloc != 0 }
func (s *section) hasData() bool { return s.typ != shtNobits && s.size > 0 }
func (s *section) fileEnd() uint64 {
	if !s.hasData() {
		return s.offset
	}
	return s.offset + s.size
}

// elfImage is a parsed-just-enough view of an ELF64 file.
type elfImage struct {
	header   []byte
	bo       binary.ByteOrder
	sections []*section
	shstrndx int
}

// parseELF64 reads the header and section table. It deliberately reads only
// what it needs: an artifact is untrusted input, and every offset below is
// bounds-checked against the real file size.
func parseELF64(f *os.File, size int64) (*elfImage, error) {
	if size < elfHeaderSize64 {
		return nil, ErrNotELF
	}
	hdr := make([]byte, elfHeaderSize64)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		return nil, fmt.Errorf("read elf header: %w", err)
	}
	if !bytes.Equal(hdr[:4], elfMagic) {
		return nil, ErrNotELF
	}
	if hdr[4] != 2 { // EI_CLASS: ELFCLASS64
		return nil, ErrUnsupportedELF
	}

	var bo binary.ByteOrder
	switch hdr[5] { // EI_DATA
	case 1:
		bo = binary.LittleEndian
	case 2:
		bo = binary.BigEndian
	default:
		return nil, ErrUnsupportedELF
	}

	shoff := bo.Uint64(hdr[offShoff:])
	shentsize := bo.Uint16(hdr[offShentsize:])
	shnum := bo.Uint16(hdr[offShnum:])
	shstrndx := bo.Uint16(hdr[offShstrndx:])

	// shnum == 0 means the real count lives in section 0 (SHN_XINDEX), and
	// shstrndx == 0xffff means the same for the string table index. Neither is
	// worth supporting here.
	if shoff == 0 || shnum == 0 || shstrndx == 0xffff {
		return nil, ErrUnsupportedELF
	}
	if shentsize != sectionEntrySize {
		return nil, ErrUnsupportedELF
	}
	tableEnd := shoff + uint64(shnum)*uint64(shentsize)
	if tableEnd > uint64(size) {
		return nil, ErrUnsupportedELF
	}
	if int(shstrndx) >= int(shnum) {
		return nil, ErrUnsupportedELF
	}

	img := &elfImage{header: hdr, bo: bo, shstrndx: int(shstrndx)}
	table := make([]byte, uint64(shnum)*uint64(shentsize))
	if _, err := f.ReadAt(table, int64(shoff)); err != nil {
		return nil, fmt.Errorf("read section table: %w", err)
	}
	for i := 0; i < int(shnum); i++ {
		raw := table[i*sectionEntrySize : (i+1)*sectionEntrySize]
		s := &section{
			raw:       raw,
			typ:       bo.Uint32(raw[shType:]),
			flags:     bo.Uint64(raw[shFlags:]),
			offset:    bo.Uint64(raw[shOffset:]),
			size:      bo.Uint64(raw[shSize:]),
			addralign: bo.Uint64(raw[shAddralign:]),
		}
		if s.hasData() && s.fileEnd() > uint64(size) {
			return nil, ErrUnsupportedELF
		}
		img.sections = append(img.sections, s)
	}

	// Resolve names from .shstrtab.
	strtab := img.sections[shstrndx]
	if !strtab.hasData() {
		return nil, ErrUnsupportedELF
	}
	names := make([]byte, strtab.size)
	if _, err := f.ReadAt(names, int64(strtab.offset)); err != nil {
		return nil, fmt.Errorf("read section names: %w", err)
	}
	for _, s := range img.sections {
		off := bo.Uint32(s.raw[shName:])
		if uint64(off) >= strtab.size {
			continue
		}
		end := uint64(off)
		for end < strtab.size && names[end] != 0 {
			end++
		}
		s.name = string(names[off:end])
	}
	return img, nil
}

// strippable reports whether a section is debug/symbol baggage that `strip`
// removes. Only non-allocated sections qualify: anything the loader maps stays,
// no matter what it is called.
func (s *section) strippable() bool {
	if s.alloc() {
		return false
	}
	switch s.name {
	case ".symtab", ".strtab", ".gdb_index", ".gnu_debuglink", ".comment.SUSE.OPTs":
		return true
	}
	return strings.HasPrefix(s.name, ".debug_") ||
		strings.HasPrefix(s.name, ".zdebug_") ||
		strings.HasPrefix(s.name, ".stab")
}

// plan marks sections to keep and reports the offset up to which the original
// file is copied verbatim. Everything allocated lives below that point -- so
// program headers, which reference file offsets directly, stay valid without
// being touched at all.
func (img *elfImage) plan() (prefixEnd uint64) {
	bo := img.bo
	prefixEnd = uint64(bo.Uint16(img.header[offEhsize:]))
	if phoff := bo.Uint64(img.header[offPhoff:]); phoff > 0 {
		end := phoff + uint64(bo.Uint16(img.header[offPhnum:]))*uint64(bo.Uint16(img.header[offPhentsize:]))
		if end > prefixEnd {
			prefixEnd = end
		}
	}
	for i, s := range img.sections {
		s.keep = i == 0 || i == img.shstrndx || !s.strippable()
		if s.alloc() && s.hasData() && s.fileEnd() > prefixEnd {
			prefixEnd = s.fileEnd()
		}
	}
	// A kept non-allocated section that already sits inside the verbatim
	// prefix keeps its offset; only the ones past it are relocated.
	return prefixEnd
}

func align(n, a uint64) uint64 {
	if a <= 1 {
		return n
	}
	if rem := n % a; rem != 0 {
		return n + (a - rem)
	}
	return n
}

// writeStripped produces the stripped binary. Layout: the original bytes up to
// the end of the last allocated section, then the kept non-allocated sections,
// then the section header table.
func (img *elfImage) writeStripped(src *os.File, dst *os.File, prefixEnd uint64) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := io.CopyN(dst, src, int64(prefixEnd)); err != nil {
		return fmt.Errorf("copy allocated image: %w", err)
	}

	pos := prefixEnd
	for _, s := range img.sections {
		if !s.keep {
			continue
		}
		if !s.hasData() || s.fileEnd() <= prefixEnd {
			// Stays where it is (or has no bytes at all).
			s.newOffset = s.offset
			continue
		}
		padded := align(pos, s.addralign)
		if padded > pos {
			if _, err := dst.Write(make([]byte, padded-pos)); err != nil {
				return err
			}
			pos = padded
		}
		if _, err := src.Seek(int64(s.offset), io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(dst, src, int64(s.size)); err != nil {
			return fmt.Errorf("copy section %s: %w", s.name, err)
		}
		s.newOffset = pos
		pos += s.size
	}

	return img.writeSectionTable(dst, pos, func(s *section) bool { return s.keep }, nil,
		func(s *section) (uint64, uint32) { return s.newOffset, s.typ })
}

// writeDebug produces the companion debug file: the same section table, but
// only the stripped-out sections (plus the build-id note and the name table)
// carry bytes -- every other section becomes SHT_NOBITS. That is the shape
// `objcopy --only-keep-debug` emits and what a debugger expects to pair with
// the stripped binary.
func (img *elfImage) writeDebug(src *os.File, dst *os.File) error {
	carries := func(s *section) bool {
		if !s.hasData() {
			return false
		}
		return !s.keep || s.name == ".shstrtab" || s.name == ".note.gnu.build-id"
	}

	pos := uint64(elfHeaderSize64)
	if _, err := dst.Write(img.header); err != nil {
		return err
	}
	for _, s := range img.sections {
		if !carries(s) {
			s.newOffset = s.offset
			continue
		}
		padded := align(pos, s.addralign)
		if padded > pos {
			if _, err := dst.Write(make([]byte, padded-pos)); err != nil {
				return err
			}
			pos = padded
		}
		if _, err := src.Seek(int64(s.offset), io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(dst, src, int64(s.size)); err != nil {
			return fmt.Errorf("copy debug section %s: %w", s.name, err)
		}
		s.newOffset = pos
		pos += s.size
	}

	// The debug file carries no segments -- it is never loaded, only read by a
	// debugger -- so the inherited program-header fields must be cleared.
	// Leaving them in place points readers at offsets that no longer exist:
	// debug/elf rejects the file outright ("invalid program header offset").
	dropPhdrs := func(hdr []byte) {
		img.bo.PutUint64(hdr[offPhoff:], 0)
		img.bo.PutUint16(hdr[offPhnum:], 0)
	}
	// Every section stays in the table -- a debugger needs the full picture --
	// but only the stripped-out ones carry bytes.
	all := func(*section) bool { return true }
	return img.writeSectionTable(dst, pos, all, dropPhdrs, func(s *section) (uint64, uint32) {
		if carries(s) {
			return s.newOffset, s.typ
		}
		return s.newOffset, shtNobits
	})
}

// writeSectionTable appends the section header table at pos and patches the
// ELF header to match. kept sections keep their raw 64-byte entry with only
// sh_offset (and, for the debug file, sh_type) rewritten.
func (img *elfImage) writeSectionTable(dst *os.File, pos uint64, include func(*section) bool, patchHeader func([]byte), entry func(*section) (uint64, uint32)) error {
	bo := img.bo
	shoff := align(pos, 8)
	if shoff > pos {
		if _, err := dst.Write(make([]byte, shoff-pos)); err != nil {
			return err
		}
	}

	count := 0
	newIndex := 0
	for i, s := range img.sections {
		if !include(s) {
			continue
		}
		if i == img.shstrndx {
			newIndex = count
		}
		count++

		raw := make([]byte, sectionEntrySize)
		copy(raw, s.raw)
		off, typ := entry(s)
		bo.PutUint64(raw[shOffset:], off)
		bo.PutUint32(raw[shType:], typ)
		if _, err := dst.Write(raw); err != nil {
			return err
		}
	}

	hdr := make([]byte, elfHeaderSize64)
	copy(hdr, img.header)
	bo.PutUint64(hdr[offShoff:], shoff)
	bo.PutUint16(hdr[offShnum:], uint16(count))
	bo.PutUint16(hdr[offShstrndx:], uint16(newIndex))
	if patchHeader != nil {
		patchHeader(hdr)
	}
	if _, err := dst.WriteAt(hdr, 0); err != nil {
		return fmt.Errorf("patch elf header: %w", err)
	}
	return nil
}

// stripELF64 writes the stripped binary to strippedPath and its debug
// companion to debugPath. It returns ErrNotELF / ErrUnsupportedELF for input it
// declines to touch, in which case neither output is written.
func stripELF64(inputPath, strippedPath, debugPath string) error {
	src, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	defer src.Close()

	fi, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat input: %w", err)
	}
	img, err := parseELF64(src, fi.Size())
	if err != nil {
		return err
	}
	prefixEnd := img.plan()

	stripped, err := os.OpenFile(strippedPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := img.writeStripped(src, stripped, prefixEnd); err != nil {
		stripped.Close()
		return err
	}
	if err := stripped.Close(); err != nil {
		return err
	}

	debug, err := os.OpenFile(debugPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := img.writeDebug(src, debug); err != nil {
		debug.Close()
		return err
	}
	return debug.Close()
}
