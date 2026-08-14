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

// SniffLen is how many leading bytes Detect needs.
const SniffLen = 6

// Detect classifies a file from its leading bytes. It returns "" for anything
// it does not recognize; callers decide what an unrecognized file may claim.
func Detect(head []byte) Format {
	if len(head) >= len(apeMagic) && string(head[:len(apeMagic)]) == string(apeMagic) {
		return APE
	}
	return ""
}
