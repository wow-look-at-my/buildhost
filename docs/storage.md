# Blob storage

`internal/storage/`. Extracted verbatim from CLAUDE.md. Paragraph breaks were added at the existing topic boundaries, no wording changed.

Content-addressed blob storage (filesystem backend, zstd-compressed, key validation).

`Get` **memory-maps the compressed blob** (via `github.com/wow-look-at-my/go-mmap`, mapping the `os.Root`-opened fd to keep the path-traversal sandbox) and returns a streaming zstd decoder reading off the mapping, so a read never loads the whole artifact into. Uncompressed blobs are served straight from the mapping. An empty blob returns an empty reader.

`GetCompressed` (the optional `CompressedGetter` capability, implemented by `Filesystem` and forwarded by `TracedStorage`) returns the stored bytes **without** decompressing -- the raw zstd stream for a compressed blob (Encoding `zstd`), or the identity bytes for one stored raw.

Two further optional capabilities serve indexed containers: `PutUncompressed` (`UncompressedPutter`) stores a blob without the whole-blob zstd wrapper -- content that compresses itself per block must stay.

Storage keys are validated as hex SHA-256 to prevent path traversal. The storage layer rejects symlinks via an Lstat check.

## Bounded memory

Blob reads are mmap-backed and decoded as a stream, and every download/repackage path streams (no `io.ReadAll` of an artifact, no whole-archive `bytes.Buffer`), so per-request memory is bounded by the compressor window rather. The server also sets `GOMEMLIMIT` from the container's memory cgroup at startup (`automemlimit`, 0.9 ratio. No-ops if `GOMEMLIMIT`/`AUTOMEMLIMIT=off` is set). So the GC runs harder near the limit instead of letting the heap grow into an OOM-kill. Together these let buildhost serve artifacts far larger than its `mem_limit`.
