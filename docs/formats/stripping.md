# Binary debug-info stripping

`internal/strip/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

stripping of the Binary debug info, implemented **natively in Go** (`elf.go`). It does NOT shell out to strip(1) or objcopy(1) any more. `StripReader` and `StripReaderDebug` spool a reader to a temp, run the file-based `Strip`, and stream the stripped or debug file back.

## Why native

The production image is distroless and ships no binutils. The old shell-out `Available()` was therefore always false there. Stripping silently no-opped in production for weeks, and live `dl.pazer.build` still answers `X-Debug-Symbols: unavailable` on every artifact. `fmt=symbols` did not work at all. The documented design says stripping happens at download time. In-process makes the behavior same everywhere buildhost runs.

It is also the permanent fix for the BFD hole. strip. Objcopy accept PE/COFF and Mach-O, and they REWRITE them instead of a failure. That is how a Cosmopolitan APE artifact, a well-formed PE32+ to BFD, was served at about half size, corrupt and NON-REPRODUCIBLE. Two GETs of one immutable URL differed. A Homebrew formula's cached sha256 can never match it. `brew install` then failed the checksum on any host with binutils, and only the distroless prod image was spared.

`Strip` parses ELF64 itself and refuses anything else (`ErrNotELF`, `ErrUnsupportedELF`). A non-ELF artifact is therefore served byte-for-byte as uploaded.

## What it does

Drop the non-`SHF_ALLOC` debug and symbol sections (`.debug_*`, `.zdebug_*`, `.stab*`, `.symtab`, `.strtab`). Keep everything else. Rewrite the section table. An allocated section NEVER moves. A program header addresses it by file offset, so a relocation breaks execution. The whole transformation is therefore "copy the allocated prefix verbatim, re-emit the rest".

`.shstrtab` is kept as it is. A stripped file therefore still contains the NAMES of the dropped sections as bytes. A substring search for `.debug_info` is not a valid check. Read the section table instead. The debug companion mirrors `objcopy --only-keep-debug`. Every section header is retained, an allocated one becomes `SHT_NOBITS`, and the program-header fields are cleared.

Leaving those fields makes the file unparseable.

Tests EXECUTE the stripped binary instead of an inspection of it. They pin determinism, because a download's sha256 is baked into a Homebrew formula and the APT index. They also pin the invariant that an allocated section keeps its offset. Failures are never swallowed. `LogSkipped` logs a non-ELF skip at debug and any other failure at WARN. Silence at these call sites is exactly what hid the breakage for weeks.

Both download paths peek the ELF magic with `LooksELF` BEFORE they spool. A non-strippable artifact therefore costs no disk round-trip and keeps its zstd passthrough.

## Packing

The identity (uncompressed) download of an executable is stripped and then packed with UPX (`internal/pack`). Packing a stripped Go binary takes it to roughly two thirds of the upload and removes the debug payload in one step.

The packer ships INSIDE the server binary, fetched at build time by `internal/pack/upxgen` and embedded zstd-compressed. The production image is distroless and has no binutils, so a packer looked up on `PATH` would silently stop packing there, exactly as the old `strip(1)` shell-out did. `upxgen` pins the release and the SHA-256 of every download.

A packed output is only served after it is proven to be the same program. `Packer.Pack` unpacks the result and requires the result of THAT to be the stripped input byte for byte, and it requires the packed file to still be the input's container format. Any failure, or an output that would not be smaller, leaves the caller with the stripped bytes it already trusts. Packing an already-packed input therefore cannot corrupt anything.

An APE is offered to the packer and served unchanged when the packer refuses it or when the result no longer identifies as an APE. The refusal is remembered per artifact, so a repeated download of an immutable URL neither repeats the attempt nor returns different bytes.

Packing is deterministic for an input and a packer release, and every cached variant is keyed by the payload transform version. A change to either step therefore invalidates the cached bodies rather than serving an old shape.
