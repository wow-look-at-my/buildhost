# Binary debug-info stripping

`internal/strip/`. Extracted verbatim from CLAUDE.md; paragraph breaks were added
at the existing topic boundaries, no wording changed.

Binary debug info stripping, implemented **natively in Go** (`elf.go`); it does
NOT shell out to strip(1)/objcopy(1) any more. `StripReader`/`StripReaderDebug`
spool a reader to a temp, run the file-based `Strip`, and stream the
stripped/debug file back.

## Why native

The production image is distroless and ships no binutils, so the old shell-out
`Available()` was always false there -- stripping silently no-opped in production
for weeks (live `dl.pazer.build` still answers `X-Debug-Symbols: unavailable` on
every artifact) and `fmt=symbols` could not work at all, while the documented
design says stripping happens at download time. In-process makes the behavior
identical everywhere buildhost runs.

It is also the permanent fix for the BFD hole: strip/objcopy accept PE/COFF and
Mach-O and REWRITE them rather than failing, which is how a Cosmopolitan APE
artifact (a well-formed PE32+ to BFD) was served ~half-size, corrupt and
NON-REPRODUCIBLE -- two GETs of one immutable URL differed, so a Homebrew
formula's cached sha256 could never match and `brew install` failed the checksum
on any host with binutils (only the distroless prod image was spared). `Strip`
parses ELF64 itself and refuses anything else (`ErrNotELF`, `ErrUnsupportedELF`),
so non-ELF artifacts are served byte-for-byte as uploaded.

## What it does

Drop non-`SHF_ALLOC` debug/symbol sections (`.debug_*`, `.zdebug_*`, `.stab*`,
`.symtab`, `.strtab`), keep everything else, rewrite the section table. Allocated
sections NEVER move -- program headers address them by file offset, so relocating
one would break execution -- which makes the whole transformation "copy the
allocated prefix verbatim, re-emit the rest". `.shstrtab` is kept as-is, so a
stripped file still contains the NAMES of dropped sections as bytes (a substring
search for `.debug_info` is therefore not a valid check; read the section table).
The debug companion mirrors `objcopy --only-keep-debug`: every section header
retained, allocated ones turned to `SHT_NOBITS`, program-header fields cleared
(leaving them makes the file unparseable).

Tests EXECUTE the stripped binary rather than just inspecting it, and pin
determinism (a download's sha256 is baked into Homebrew formulas and the APT
index) plus the invariant that allocated sections keep their offsets. Failures are
never swallowed: `LogSkipped` logs a non-ELF skip at debug and any other failure
at WARN -- silence at these two call sites is exactly what hid the breakage for
weeks. Both download paths peek the ELF magic (`LooksELF`) BEFORE spooling, so a
non-strippable artifact costs no disk round-trip and keeps its zstd passthrough.
