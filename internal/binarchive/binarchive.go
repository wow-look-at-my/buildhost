// Package binarchive stores a directory of files as a binpazer container: one
// block per file, each compressed on its own, plus a directory block that maps
// path -> block offset, and binpazer's Block Index + footer to find it.
//
// It exists because a .tar.gz cannot answer "give me this one file". A gzip
// stream has no index, so reaching a member means inflating everything ahead of
// it -- which is why serving one file out of a stored site archive used to scan
// the whole tar, per request. Here the same read is: footer -> index ->
// directory -> seek -> decode one block.
package binarchive

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	binpazer "github.com/wow-look-at-my/bin-file-fmt/go"
)

// Block type ids and their global GUIDs. binpazer interns the GUIDs to these
// small per-file ids; the GUIDs are what identify the types across programs.
const (
	typeEntry uint16 = 1 // one archived file
	typeDir   uint16 = 2 // the path -> offset directory
)

var (
	entryGUID  = binpazer.GUID{0x62, 0x75, 0x69, 0x6c, 0x64, 0x68, 0x6f, 0x73, 0x74, 0, 0, 0, 0, 0, 0, 1}
	dirGUID    = binpazer.GUID{0x62, 0x75, 0x69, 0x6c, 0x64, 0x68, 0x6f, 0x73, 0x74, 0, 0, 0, 0, 0, 0, 2}
	writerGUID = binpazer.GUID{0x62, 0x75, 0x69, 0x6c, 0x64, 0x68, 0x6f, 0x73, 0x74, 0, 0, 0, 0, 0, 0, 0}
)

// Magic is the leading bytes of every binpazer file: enough to tell an archive
// from the plain tar blobs written before this format was adopted.
const Magic = binpazer.Magic

// IsArchive reports whether a blob's leading bytes are a binpazer container.
func IsArchive(head []byte) bool {
	return len(head) >= len(Magic) && string(head[:len(Magic)]) == Magic
}

// Entry describes one archived file.
type Entry struct {
	Path string `json:"path"`
	Size int64  `json:"size"` // decompressed size
	Mode uint32 `json:"mode"`
	// Offset is the entry block's absolute offset in the container, which is
	// what turns a path lookup into a seek.
	Offset uint64 `json:"offset"`
}

// directory is the payload of the directory block.
type directory struct {
	Entries []Entry `json:"entries"`
}

// zstdCodec teaches binpazer the zstd codec (SPEC registry id 3). The library
// itself ships only what the Go standard library provides, so every other
// codec arrives this way.
type zstdCodec struct{}

func (zstdCodec) NewReader(r io.Reader) (io.ReadCloser, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	return zr.IOReadCloser(), nil
}

func (zstdCodec) NewWriter(w io.Writer) (io.WriteCloser, error) {
	return zstd.NewWriter(w)
}

var registerOnce sync.Once

func registerCodecs() {
	registerOnce.Do(func() {
		if err := binpazer.RegisterCodec(binpazer.CodecZstd, zstdCodec{}); err != nil {
			panic("binarchive: registering the zstd codec: " + err.Error())
		}
	})
}

// MaxEntries and MaxTotalSize bound what a single archive may hold; they mirror
// the caller's own upload limits so a hostile tar cannot make the writer
// allocate without end.
type Limits struct {
	MaxEntries   int
	MaxTotalSize int64
}

// Stats reports what WriteFromTar stored.
type Stats struct {
	Files int
	Bytes int64 // total decompressed bytes
}

// WriteFromTar reads a tar stream and writes a binpazer archive to w. Each
// regular file becomes one zstd-compressed block; the directory block and the
// Block Index are written last, so a reader finds any file with two seeks.
//
// w must be seekable: binpazer back-patches the file length and, because the
// archive uses compressed blocks, the header's version_minor.
func WriteFromTar(w io.WriteSeeker, tr *tar.Reader, lim Limits) (*Stats, error) {
	registerCodecs()

	bw, err := binpazer.NewWriter(w, writerGUID, "buildhost", []binpazer.TypeDef{
		{TypeID: typeEntry, GUID: entryGUID, Name: "File"},
		{TypeID: typeDir, GUID: dirGUID, Name: "Directory"},
	})
	if err != nil {
		return nil, err
	}

	var (
		dir   directory
		stats Stats
		seen  = map[string]bool{}
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue // directories and links carry no servable bytes
		}
		name := path.Clean(hdr.Name)
		if seen[name] {
			continue // last writer wins in tar; keep the first, as a scan would
		}
		if lim.MaxEntries > 0 && len(dir.Entries) >= lim.MaxEntries {
			return nil, fmt.Errorf("archive holds more than %d files", lim.MaxEntries)
		}
		if lim.MaxTotalSize > 0 && stats.Bytes+hdr.Size > lim.MaxTotalSize {
			return nil, fmt.Errorf("archive exceeds %d bytes", lim.MaxTotalSize)
		}

		// The payload is read whole because a block is written whole; the tar
		// entry's own size bounds it, and the caller's limits bound that.
		body := make([]byte, hdr.Size)
		if _, err := io.ReadFull(tr, body); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		offset := bw.Offset()
		if err := bw.CompressedBlock(typeEntry, binpazer.FlagHasCRC, binpazer.CodecZstd, body); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		seen[name] = true
		dir.Entries = append(dir.Entries, Entry{
			Path: name, Size: hdr.Size, Mode: uint32(hdr.Mode), Offset: offset,
		})
		stats.Files++
		stats.Bytes += hdr.Size
	}

	// Sorted so a reader can binary-search it, and so the same input always
	// produces the same directory bytes.
	sort.Slice(dir.Entries, func(i, j int) bool { return dir.Entries[i].Path < dir.Entries[j].Path })
	payload, err := json.Marshal(dir)
	if err != nil {
		return nil, err
	}
	if err := bw.CompressedBlock(typeDir, binpazer.FlagCritical|binpazer.FlagHasCRC, binpazer.CodecZstd, payload); err != nil {
		return nil, fmt.Errorf("write directory: %w", err)
	}
	if err := bw.WriteIndex(); err != nil {
		return nil, err
	}
	if err := bw.End(); err != nil {
		return nil, err
	}
	return &stats, nil
}

// Archive is an opened archive. Its directory is read once; every file read
// afterwards is a seek plus one block decode, so an Archive is cheap to keep
// and safe to use from several goroutines (each read gets its own reader over
// the shared io.ReaderAt).
type Archive struct {
	ra      io.ReaderAt
	size    int64
	entries []Entry
	byPath  map[string]Entry
}

// Open reads an archive's directory. ra must be safe for concurrent use (an
// mmap'd blob is).
func Open(ra io.ReaderAt, size int64) (*Archive, error) {
	registerCodecs()

	br, err := binpazer.NewReader(io.NewSectionReader(ra, 0, size))
	if err != nil {
		return nil, err
	}
	offsets, err := br.Find(typeDir)
	if err != nil {
		return nil, fmt.Errorf("find directory: %w", err)
	}
	if len(offsets) == 0 {
		return nil, fmt.Errorf("archive has no directory block")
	}
	b, err := br.At(offsets[len(offsets)-1])
	if err != nil {
		return nil, err
	}
	payload, err := br.ReadDecompressedPayload(b)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	var dir directory
	if err := json.Unmarshal(payload, &dir); err != nil {
		return nil, fmt.Errorf("parse directory: %w", err)
	}
	a := &Archive{ra: ra, size: size, entries: dir.Entries, byPath: make(map[string]Entry, len(dir.Entries))}
	for _, e := range dir.Entries {
		a.byPath[e.Path] = e
	}
	return a, nil
}

// Entries returns every file in the archive, sorted by path.
func (a *Archive) Entries() []Entry { return a.entries }

// Lookup reports the entry for a path, without reading its bytes.
func (a *Archive) Lookup(p string) (Entry, bool) {
	e, ok := a.byPath[path.Clean(strings.TrimPrefix(p, "./"))]
	return e, ok
}

// ErrNotFound is returned by Open for a path the archive does not hold.
var ErrNotFound = os.ErrNotExist

// OpenFile returns a reader over one file's decompressed bytes. It is O(1) in
// the number of files: the entry's offset comes from the directory, and only
// that block is decoded.
func (a *Archive) OpenFile(p string) (io.Reader, Entry, error) {
	e, ok := a.Lookup(p)
	if !ok {
		return nil, Entry{}, ErrNotFound
	}
	br, err := binpazer.NewReader(io.NewSectionReader(a.ra, 0, a.size))
	if err != nil {
		return nil, e, err
	}
	b, err := br.At(e.Offset)
	if err != nil {
		return nil, e, err
	}
	if b.TypeID != typeEntry {
		return nil, e, fmt.Errorf("directory points at a type-%d block, not a file", b.TypeID)
	}
	r, err := br.DecompressedPayloadReader(b)
	if err != nil {
		return nil, e, err
	}
	return r, e, nil
}

// WriteTar rebuilds a tar stream from the archive, so a caller that must hand
// out the original packaging (an npm .tgz, a browser download) can regenerate
// it from the indexed form.
func (a *Archive) WriteTar(w io.Writer) error {
	tw := tar.NewWriter(w)
	for _, e := range a.entries {
		mode := int64(e.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: e.Path, Mode: mode, Size: e.Size, Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		r, _, err := a.OpenFile(e.Path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, r); err != nil {
			return err
		}
	}
	return tw.Close()
}
