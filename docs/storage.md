# Blob storage

`internal/storage/`. Extracted verbatim from CLAUDE.md. Paragraph breaks go at the existing topic boundaries. No wording changed.

Content-addressed blob storage (filesystem backend, zstd-compressed, key validation).

`Get` **memory-maps the compressed blob** through `github.com/wow-look-at-my/go-mmap`. It maps the `os.Root`-opened fd, which keeps the path-traversal sandbox. It returns a streaming zstd decoder that reads off the mapping. A read therefore never loads the whole artifact into the heap. The decoder pulls kernel-paged pages on demand (`MADV_SEQUENTIAL`), and Close unmaps. An uncompressed blob is served straight from the mapping. An empty blob returns an empty reader.

`GetCompressed` returns the stored bytes **without** a decompression step. It is the optional `CompressedGetter` capability, implemented by `Filesystem` and forwarded by `TracedStorage`. For a compressed blob that is the raw zstd stream (Encoding `zstd`). For one stored raw it is the identity bytes. A handler can therefore pass `Content-Encoding: zstd` straight through to a zstd-accepting client and skip server-side decompression entirely.

Two further optional capabilities serve indexed containers. `PutUncompressed` (`UncompressedPutter`) stores a blob without the whole-blob zstd wrapper, because content that compresses itself per block must stay seekable. `OpenReaderAt` (`RandomGetter`) mmaps a blob for reads at an offset. It reports `ErrRandomUnsupported` for a compressed blob, and the caller falls back to `Get`.

Storage keys are validated as hex SHA-256 to prevent path traversal. The storage layer rejects symlinks via an Lstat check.

## Bounded memory

A blob read is mmap-backed and decoded as a stream. Every download and repackage path streams too. There is no `io.ReadAll` of an artifact and no whole-archive `bytes.Buffer`. Per-request memory is therefore bounded by the compressor window instead of the artifact size. The server also sets `GOMEMLIMIT` from the container's memory cgroup at startup (`automemlimit`, 0.9 ratio). It no-ops when `GOMEMLIMIT` or `AUTOMEMLIMIT=off` is set. The GC then runs harder near the limit instead of a heap that grows into an OOM-kill. Together these let buildhost serve artifacts far larger than its `mem_limit`.
