// Package exeformat identifies an executable's container format from its
package exeformat

// Format names a recognized executable container. Empty means unrecognized.
type Format string

// APE is an Actually Portable Executable: a polyglot shell script / PE / ELF /
const APE Format = "ape"

// Label is the badge text for a format, e.g. "APE".
func (f Format) Label() string {
	switch f {
	case APE:
		return "APE"
	}
	return ""
}

func (f Format) MultiPlatformCapable() bool { return f == APE }

var apeMagic = []byte("MZqFpD")

// SniffLen is how many leading bytes the checks here need. The format check
const SniffLen = 512

// Offsets in the DOS/PE header chain, all fixed by the PE format itself rather
const (
	elfanewOff = 0x3c // DOS header -> file offset of the PE signature
	peNsectOff = 6    // PE signature + COFF header -> NumberOfSections
)

// NTBoot describes whether an APE's PE header can actually boot the binary on
type NTBoot int

const (
	// NTUnknown means the sniffed prefix does not settle the question.
	NTUnknown NTBoot = iota
	// NTStub is the do-nothing stub header: it keeps the file parseable as a
	NTStub
	// NTReal is a header that maps the payload and enters the runtime.
	NTReal
)

// DetectNTBoot classifies an APE's PE header from the sniffed prefix.
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
func Detect(head []byte) Format {
	if len(head) >= len(apeMagic) && string(head[:len(apeMagic)]) == string(apeMagic) {
		return APE
	}
	return ""
}
