# Static sites are stored as indexed binpazer archives

## The problem

Serving one file out of a site meant scanning the stored tar from the start,
per request -- and twice when the request missed, because finding `404.html`
was a second scan (`internal/sites/serve.go`, before this change). A `.tar.gz`
is one DEFLATE stream with no index; there is no other way to read it. The cost
of serving *any* file grew with the size of the site around it, forever.

The same defect, in a different place, is what made the npm registry look hung:
a packument had to inflate 33 MB of a stored tarball to reach a 191-byte
`package.json` (see `docs/npm-packument-manifest-cache.md`).

## What is stored now

A [binpazer](https://github.com/wow-look-at-my/bin-file-fmt) container
(`internal/binarchive`):

- one **zstd-compressed block per file** (block type `File`, GUID
  `62756...0001`),
- a **directory block** (`Directory`, `...0002`) mapping path -> block offset,
  size and mode, written last because it needs the offsets,
- binpazer's **Block Index + footer**, which is how a reader finds the
  directory without walking.

Serving a file is: footer -> index -> directory -> seek -> decode one block.

**The upload contract did not change.** Publishers still send `tar.gz` or
`zip`, the same validation and caps run, and `sites.sha256` still describes the
canonical tar the uploader sent. Fixing a format's shortcomings is buildhost's
job, not the publisher's; nothing about the client changed.

**Nothing needs migrating.** Sites uploaded before this are plain tar blobs, and
the read path sniffs the container magic (`binpazer`) and falls back to the
scan for them. A blob the storage backend cannot read at an offset falls back
the same way.

## Supporting storage capabilities

`internal/storage` gained two optional capabilities alongside
`CompressedGetter`:

- **`UncompressedPutter.PutUncompressed`** -- store a blob without the storage
  layer's whole-blob zstd wrapper. An archive already compresses every block;
  wrapping it again would trade the index away for nothing, since a zstd stream
  has no offsets to seek to.
- **`RandomGetter.OpenReaderAt`** -- mmap a blob and read it at an offset.
  A compressed blob reports `ErrRandomUnsupported`, which is what makes the
  fallback above automatic rather than a flag.

## Measured

| | before (tar) | after (archive) |
|---|---|---|
| read one file from a ~1 MB container | whole container | **~128 KiB, constant** |
| ...when the container grows 10x | 10x the work | **unchanged** |
| a miss (404 page) | a SECOND full scan | one more seek |

The 128 KiB is two of the reader's 64 KiB buffer fills -- one to parse the
header and type table, one at the seek target -- plus the entry. It does not
move when the archive grows, which is the whole point;
`TestReadCostIsIndependentOfArchiveSize` pins exactly that by counting the
bytes pulled out of the container.

## The trade-off: per-file compression gives up cross-file redundancy

Compressing each file on its own is what makes one file readable without
touching the others. It also gives up what a single gzip stream over the whole
tar exploits: the redundancy *between* files.

`TestArchiveSizeVsTarGz` measures the worst case on purpose -- 60 near-identical
HTML pages, where nearly all the compressible information is cross-file:

```
tar 146,944 bytes,  tar.gz 2,016 bytes,  binpazer archive 12,008 bytes
```

About **6x** on that fixture. A site with varied content (real HTML, CSS, JS,
images) costs far less, because most of its compressible information is inside
each file rather than between files. The test guards against ballooning past
8x rather than asserting a number that only holds for one fixture.

**If that cost matters, the fix is a shared dictionary, not giving up the
index.** zstd supports compressing many small payloads against a dictionary
trained on the corpus, which recovers most cross-file redundancy while keeping
every entry independently decodable. It would fit binpazer's Compression
Envelope through the `codec_id = 65535` GUID escape (a codec meaning "zstd with
this archive's dictionary"), so no format change is needed -- a reader without
that codec reports `ErrCodecUnavailable` rather than silently misreading, which
is the contract the escape exists for. Deliberately not done here: it is a real
design with its own tests, and the storage cost it addresses has not been shown
to matter yet.
