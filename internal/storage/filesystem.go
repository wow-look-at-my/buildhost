package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/klauspost/compress/zstd"
	mmap "github.com/wow-look-at-my/go-mmap"
)

var (
	validStorageKey = regexp.MustCompile(`^[a-f0-9]{64}$`)
	compressedMagic = [4]byte{'B', 'H', 'C', 1}
)

type Filesystem struct {
	root     *os.Root
	compress bool
}

func NewFilesystem(rootPath string, compress bool) (*Filesystem, error) {
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return &Filesystem{root: root, compress: compress}, nil
}

func (fs *Filesystem) Put(_ context.Context, r io.Reader) (string, int64, error) {
	rawTmp, err := os.CreateTemp(fs.root.Name(), ".upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	rawBase := filepath.Base(rawTmp.Name())
	defer func() {
		rawTmp.Close()
		fs.root.Remove(rawBase)
	}()

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(rawTmp, h), r)
	if err != nil {
		return "", 0, fmt.Errorf("write temp file: %w", err)
	}

	key := hex.EncodeToString(h.Sum(nil))
	rel := fs.rel(key)

	if _, err := fs.root.Stat(rel); err == nil {
		return key, size, nil
	}

	if err := fs.root.MkdirAll(key[:2], 0o755); err != nil {
		return "", 0, fmt.Errorf("create shard dir: %w", err)
	}

	if !fs.compress {
		if err := rawTmp.Close(); err != nil {
			return "", 0, fmt.Errorf("close temp file: %w", err)
		}
		if err := fs.root.Rename(rawBase, rel); err != nil {
			return "", 0, fmt.Errorf("rename to final: %w", err)
		}
		return key, size, nil
	}

	if _, err := rawTmp.Seek(0, io.SeekStart); err != nil {
		return "", 0, fmt.Errorf("seek temp file: %w", err)
	}

	cmpTmp, err := os.CreateTemp(fs.root.Name(), ".compress-*")
	if err != nil {
		return "", 0, fmt.Errorf("create compress temp: %w", err)
	}
	cmpBase := filepath.Base(cmpTmp.Name())
	defer func() {
		cmpTmp.Close()
		fs.root.Remove(cmpBase)
	}()

	if _, err := cmpTmp.Write(compressedMagic[:]); err != nil {
		return "", 0, fmt.Errorf("write magic: %w", err)
	}
	if err := binary.Write(cmpTmp, binary.LittleEndian, size); err != nil {
		return "", 0, fmt.Errorf("write size header: %w", err)
	}
	zw, err := zstd.NewWriter(cmpTmp)
	if err != nil {
		return "", 0, fmt.Errorf("create compressor: %w", err)
	}
	if _, err := io.Copy(zw, rawTmp); err != nil {
		zw.Close()
		return "", 0, fmt.Errorf("compress data: %w", err)
	}
	if err := zw.Close(); err != nil {
		return "", 0, fmt.Errorf("finalize compression: %w", err)
	}
	if err := cmpTmp.Close(); err != nil {
		return "", 0, fmt.Errorf("close compress temp: %w", err)
	}

	if err := fs.root.Rename(cmpBase, rel); err != nil {
		return "", 0, fmt.Errorf("rename to final: %w", err)
	}
	return key, size, nil
}

func (fs *Filesystem) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	if !validStorageKey.MatchString(key) {
		return nil, 0, os.ErrNotExist
	}
	f, err := fs.root.Open(fs.rel(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, os.ErrNotExist
		}
		return nil, 0, fmt.Errorf("open blob: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("stat blob: %w", err)
	}
	size := info.Size()

	// An empty blob has nothing to map (mmap rejects a zero-length region).
	if size == 0 {
		f.Close()
		return io.NopCloser(bytes.NewReader(nil)), 0, nil
	}

	// Memory-map the (compressed) blob and read through the mapping. The fd may be
	// closed once mapped -- the mapping keeps the file alive until Unmap. Sequential
	// advice lets the kernel read ahead and drop pages behind the cursor, so even a
	// huge blob streams from a reclaimable mapping instead of being read into the heap.
	m, err := mmap.MapRegion(int(f.Fd()), size, mmap.ProtRead, mmap.MapShared, 0)
	f.Close()
	if err != nil {
		return nil, 0, fmt.Errorf("mmap blob: %w", err)
	}
	_ = m.Advise(mmap.AdvSequential)

	// Compressed blobs carry a 4-byte magic + 8-byte little-endian original-size
	// header, then a zstd stream. Decode straight off the mapping: the decoder pulls
	// compressed pages on demand and emits decompressed chunks as the caller reads.
	if len(m) >= 12 && bytes.Equal(m[:4], compressedMagic[:]) {
		origSize := int64(binary.LittleEndian.Uint64(m[4:12]))
		zr, err := zstd.NewReader(bytes.NewReader(m[12:]))
		if err != nil {
			_ = m.Unmap()
			return nil, 0, fmt.Errorf("create decompressor: %w", err)
		}
		return &mmapZstdReadCloser{dec: zr, m: m}, origSize, nil
	}

	// Uncompressed blob: the mapping is the artifact. NewReader.Close unmaps it.
	return mmap.NewReader(m), size, nil
}

// mmapZstdReadCloser streams a zstd-compressed blob straight off its memory mapping.
// Close releases the decoder and unmaps the original region.
type mmapZstdReadCloser struct {
	dec *zstd.Decoder
	m   mmap.MMap
}

func (z *mmapZstdReadCloser) Read(p []byte) (int, error) { return z.dec.Read(p) }

func (z *mmapZstdReadCloser) Close() error {
	z.dec.Close()
	return z.m.Unmap()
}

func (fs *Filesystem) Delete(_ context.Context, key string) error {
	if !validStorageKey.MatchString(key) {
		return nil
	}
	err := fs.root.Remove(fs.rel(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (fs *Filesystem) Exists(_ context.Context, key string) (bool, error) {
	if !validStorageKey.MatchString(key) {
		return false, nil
	}
	_, err := fs.root.Stat(fs.rel(key))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (fs *Filesystem) rel(key string) string {
	return key[:2] + "/" + key
}
