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
	root string
}

func NewFilesystem(root string) (*Filesystem, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return &Filesystem{root: root}, nil
}

func (fs *Filesystem) Put(_ context.Context, r io.Reader) (string, int64, error) {
	tmp, err := os.CreateTemp(fs.root, ".upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
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
	dest := fs.path(key)

	if _, err := os.Stat(dest); err == nil {
		return key, size, nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", 0, fmt.Errorf("create shard dir: %w", err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", 0, fmt.Errorf("rename to final: %w", err)
	}
	return key, size, nil
}

func (fs *Filesystem) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	if !validStorageKey.MatchString(key) {
		return nil, 0, os.ErrNotExist
	}
	f, err := os.Open(fs.path(key))
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
	err := os.Remove(fs.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (fs *Filesystem) Exists(_ context.Context, key string) (bool, error) {
	if !validStorageKey.MatchString(key) {
		return false, nil
	}
	_, err := os.Stat(fs.path(key))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (fs *Filesystem) path(key string) string {
	return filepath.Join(fs.root, key[:2], key)
}
