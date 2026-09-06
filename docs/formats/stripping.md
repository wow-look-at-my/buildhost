# Binary debug-info stripping

`internal/strip/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

Binary debug info stripping, implemented **natively in Go** (`elf.go`). It does NOT shell out to strip(1) or objcopy(1) any more. `StripReader` and `StripReaderDebug` spool a reader to a temp, run the file-based `Strip`, and stream the stripped or debug file back.

## Why native

The production image is distroless and ships no binutils. The old shell-out `Available()` was therefore always false there. Stripping silently no-opped in production for weeks, and live `dl.pazer.build` still answers `X-Debug-Symbols: unavailable` on every artifact. `fmt=symbols` did not work at all. The documented design says stripping happens at download time. In-process makes the behavior identical everywhere buildhost runs.

It is also the permanent fix for the BFD hole. strip and objcopy accept PE/COFF and Mach-O, and they REWRITE them instead of a failure. That is how a Cosmopolitan APE artifact, a well-formed PE32+ to BFD, was served at about half size, corrupt and NON-REPRODUCIBLE. Two GETs of one immutable URL differed. A Homebrew formula's cached sha256 can never match it. `brew install` then failed the checksum on any host with binutils, and only the distroless prod image was spared. `Strip` parses ELF64 itself and refuses anything else (`ErrNotELF`, `ErrUnsupportedELF`). A non-ELF artifact is therefore served byte-for-byte as uploaded.

## What it does

Drop the non-`SHF_ALLOC` debug and symbol sections (`.debug_*`, `.zdebug_*`, `.stab*`, `.symtab`, `.strtab`). Keep everything else. Rewrite the section table. An allocated section NEVER moves. A program header addresses it by file offset, so a relocation breaks execution. The whole transformation is therefore "copy the allocated prefix verbatim, re-emit the rest". `.shstrtab` is kept as it is. A stripped file therefore still contains the NAMES of the dropped sections as bytes. A substring search for `.debug_info` is not a valid check. Read the section table instead. The debug companion mirrors `objcopy --only-keep-debug`. Every section header is retained, an allocated one becomes `SHT_NOBITS`, and the program-header fields are cleared. Leaving those fields makes the file unparseable.

Tests EXECUTE the stripped binary instead of an inspection of it. They pin determinism, because a download's sha256 is baked into a Homebrew formula and the APT index. They also pin the invariant that an allocated section keeps its offset. Failures are never swallowed. `LogSkipped` logs a non-ELF skip at debug and any other failure at WARN. Silence at these two call sites is exactly what hid the breakage for weeks. Both download paths peek the ELF magic with `LooksELF` BEFORE they spool. A non-strippable artifact therefore costs no disk round-trip and keeps its zstd passthrough.
