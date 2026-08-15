// Package exeformat identifies an executable's container format from its
// leading bytes. Multi-platform ingest uses it as a gate: declaring that one
// file runs on several platforms is only true for a format that actually
// carries several platforms' code, so an unrecognized file is rejected rather
// than published under a claim nothing backs.
package exeformat

// Format names a recognized executable container. Empty means unrecognized.
type Format string

// APE is an Actually Portable Executable: a polyglot shell script / PE / ELF /
// Mach-O image carrying native payloads for several OSes and architectures in
// one file. Cosmopolitan Libc builds these, as does the org's gosmopolitan Go
// fork.
const APE Format = "ape"

// Label is the badge text for a format, e.g. "APE".
func (f Format) Label() string {
	switch f {
	case APE:
		return "APE"
	}
	return ""
}

// MultiPlatformCapable reports whether one file of this format can genuinely
// run on more than one platform.
func (f Format) MultiPlatformCapable() bool { return f == APE }

// apeMagic is the first six bytes of every APE. Cosmopolitan's stub opens with
// the assembly-encoded `MZqFpD` sequence, which is simultaneously a valid DOS
// MZ header for Windows and a valid `#!`-less shell prologue on unix hosts.
var apeMagic = []byte("MZqFpD")

// SniffLen is how many leading bytes the checks here need. The format check
// wants six; NTBoot follows the DOS header's e_lfanew pointer to the PE header,
// which sits well inside this window in practice (0x80 in a gosmopolitan APE).
// A file whose PE header lands past it is reported NTUnknown, never rejected.
const SniffLen = 512

// Offsets in the DOS/PE header chain, all fixed by the PE format itself rather
// than by any one toolchain's layout.
const (
	elfanewOff = 0x3c // DOS header -> file offset of the PE signature
	peNsectOff = 6    // PE signature + COFF header -> NumberOfSections
)

// NTBoot describes whether an APE's PE header can actually boot the binary on
// Windows.
type NTBoot int

const (
	// NTUnknown means the sniffed prefix does not settle the question.
	NTUnknown NTBoot = iota
	// NTStub is the do-nothing stub header: it keeps the file parseable as a
	// PE but maps none of the payload, so the binary starts on Windows and
	// immediately returns 0 -- a success exit code for work that never ran.
	NTStub
	// NTReal is a header that maps the payload and enters the runtime.
	NTReal
)

// DetectNTBoot classifies an APE's PE header from the sniffed prefix.
//
// The stub carries ONE section; a header that boots needs separate code and
// data sections and so carries several (three in a gosmopolitan APE: .text,
// .rodata, .data). Keying on "exactly one section" rather than on a specific
// real count keeps APEs from other Cosmopolitan toolchains, whose real headers
// may be sectioned differently, out of the reject path.
func DetectNTBoot(head []byte) NTBoot {
	if Detect(head) != APE {
		return NTUnknown
	}
	if len(head) < elfanewOff+4 {
		return NTUnknown
	}
	peOff := int(uint32(head[elfanewOff]) | uint32(head[elfanewOff+1])<<8 |
		uint32(head[elfanewOff+2])<<16 | uint32(head[elfanewOff+3])<<24)
	nsectOff := peOff + peNsectOff
	if peOff < 0 || nsectOff+2 > len(head) {
		return NTUnknown
	}
	if nsect := uint16(head[nsectOff]) | uint16(head[nsectOff+1])<<8; nsect == 1 {
		return NTStub
	}
	return NTReal
}

// Detect classifies a file from its leading bytes. It returns "" for anything
// it does not recognize; callers decide what an unrecognized file may claim.
func Detect(head []byte) Format {
	if len(head) >= len(apeMagic) && string(head[:len(apeMagic)]) == string(apeMagic) {
		return APE
	}
	return ""
}
