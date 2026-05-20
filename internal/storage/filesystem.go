package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var validStorageKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

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
	tmp, err := os.CreateTemp(fs.root.Name(), ".upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpBase := filepath.Base(tmp.Name())
	defer func() {
		tmp.Close()
		fs.root.Remove(tmpBase)
	}()

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return "", 0, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("close temp file: %w", err)
	}

	key := hex.EncodeToString(h.Sum(nil))
	rel := fs.rel(key)

	if _, err := fs.root.Stat(rel); err == nil {
		return key, size, nil
	}

	if err := fs.root.MkdirAll(key[:2], 0o755); err != nil {
		return "", 0, fmt.Errorf("create shard dir: %w", err)
	}
	if err := fs.root.Rename(tmpBase, rel); err != nil {
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
	return f, info.Size(), nil
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
