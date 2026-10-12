package binarchive

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	binpazer "github.com/wow-look-at-my/bin-file-fmt/go"
	"github.com/wow-look-at-my/go-containers/set"
)

// Block type ids and their global GUIDs. binpazer interns the GUIDs to these
const (
	typeEntry uint16 = 1
	typeDir   uint16 = 2 // the path -> offset directory
)

var (
	entryGUID  = binpazer.GUID{0x62, 0x75, 0x69, 0x6c, 0x64, 0x68, 0x6f, 0x73, 0x74, 0, 0, 0, 0, 0, 0, 1}
	dirGUID    = binpazer.GUID{0x62, 0x75, 0x69, 0x6c, 0x64, 0x68, 0x6f, 0x73, 0x74, 0, 0, 0, 0, 0, 0, 2}
	writerGUID = binpazer.GUID{0x62, 0x75, 0x69, 0x6c, 0x64, 0x68, 0x6f, 0x73, 0x74, 0, 0, 0, 0, 0, 0, 0}
)

// Magic is the leading bytes of every binpazer file: enough to tell an archive
const Magic = binpazer.Magic

// IsArchive reports whether a blob's leading bytes are a binpazer container.
func IsArchive(head []byte) bool {
	return len(head) >= len(Magic) && string(head[:len(Magic)]) == Magic
}

type Entry struct {
	Path string `json:"path"`
	Size int64  `json:"size"` // decompressed size
	Mode uint32 `json:"mode"`
	// Offset is the entry block's absolute offset in the container, which is
	Offset uint64 `json:"offset"`
}

// directory is the payload of the directory block.
type directory struct {
	Entries []Entry `json:"entries"`
}

// MaxEntries and MaxTotalSize bound what a single archive may hold; they mirror
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
func WriteFromTar(w io.WriteSeeker, tr *tar.Reader, lim Limits) (*Stats, error) {
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
		seen  = set.New[string]()
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
		if seen.Contains(name) {
			continue
		}
		if lim.MaxEntries > 0 && len(dir.Entries) >= lim.MaxEntries {
			return nil, fmt.Errorf("archive holds more than %d files", lim.MaxEntries)
		}
		if lim.MaxTotalSize > 0 && stats.Bytes+hdr.Size > lim.MaxTotalSize {
			return nil, fmt.Errorf("archive exceeds %d bytes", lim.MaxTotalSize)
		}

		// The payload is read whole because a block is written whole; the tar
		body := make([]byte, hdr.Size)
		if _, err := io.ReadFull(tr, body); err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		offset, err := bw.PutCompressed(typeEntry, binpazer.FlagHasCRC, binpazer.CodecZstd, body)
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
		seen.Add(name)
		dir.Entries = append(dir.Entries, Entry{
			Path: name, Size: hdr.Size, Mode: uint32(hdr.Mode), Offset: offset,
		})
		stats.Files++
		stats.Bytes += hdr.Size
	}

	// Sorted so a reader can binary-search it, and so the same input always
	sort.Slice(dir.Entries, func(i, j int) bool { return dir.Entries[i].Path < dir.Entries[j].Path })
	if _, err := bw.PutJSON(typeDir, binpazer.FlagCritical|binpazer.FlagHasCRC, binpazer.CodecZstd, dir); err != nil {
		return nil, fmt.Errorf("write directory: %w", err)
	}
	if err := bw.Finish(); err != nil {
		return nil, err
	}
	return &stats, nil
}

type Archive struct {
	ra      io.ReaderAt
	size    int64
	entries []Entry
	byPath  map[string]Entry
}

// Open reads an archive's directory. ra must be safe for concurrent use (an
// mmap'd blob is).
func Open(ra io.ReaderAt, size int64) (*Archive, error) {
	br, err := binpazer.NewReaderAt(ra, size)
	if err != nil {
		return nil, err
	}
	var dir directory
	if err := br.DecodeJSONLast(typeDir, &dir); err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
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

func (a *Archive) OpenFile(p string) (io.Reader, Entry, error) {
	e, ok := a.Lookup(p)
	if !ok {
		return nil, Entry{}, ErrNotFound
	}
	br, err := binpazer.NewReaderAt(a.ra, a.size)
	if err != nil {
		return nil, e, err
	}
	r, _, err := br.OpenAt(e.Offset, typeEntry)
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
