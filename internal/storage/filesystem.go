package storage

import (
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
)

var (
	validStorageKey = regexp.MustCompile(`^[a-f0-9]{64}$`)
	compressedMagic = [4]byte{'B', 'H', 'C', 1}
)

type Filesystem struct {
	root *os.Root
}

func NewFilesystem(rootPath string) (*Filesystem, error) {
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	return &Filesystem{root: root}, nil
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

	if err := fs.root.MkdirAll(key[:2], 0o755); err != nil {
		return "", 0, fmt.Errorf("create shard dir: %w", err)
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

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		if _, serr := f.Seek(0, io.SeekStart); serr != nil {
			f.Close()
			return nil, 0, fmt.Errorf("seek: %w", serr)
		}
		info, serr := f.Stat()
		if serr != nil {
			f.Close()
			return nil, 0, fmt.Errorf("stat blob: %w", serr)
		}
		return f, info.Size(), nil
	}

	if magic == compressedMagic {
		var origSize int64
		if err := binary.Read(f, binary.LittleEndian, &origSize); err != nil {
			f.Close()
			return nil, 0, fmt.Errorf("read size header: %w", err)
		}
		zr, err := zstd.NewReader(f)
		if err != nil {
			f.Close()
			return nil, 0, fmt.Errorf("create decompressor: %w", err)
		}
		return &zstdReadCloser{dec: zr, f: f}, origSize, nil
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("seek: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("stat blob: %w", err)
	}
	return f, info.Size(), nil
}

type zstdReadCloser struct {
	dec *zstd.Decoder
	f   *os.File
}

func (z *zstdReadCloser) Read(p []byte) (int, error) {
	return z.dec.Read(p)
}

func (z *zstdReadCloser) Close() error {
	z.dec.Close()
	return z.f.Close()
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
