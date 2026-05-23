package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestPutStoresContentAndReturnsCorrectKey(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	content := []byte("hello world")
	wantHash := sha256.Sum256(content)
	wantKey := hex.EncodeToString(wantHash[:])

	key, size, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	assert.Equal(t, wantKey, key)

	assert.Equal(t, int64(len(content)), size)

}

func TestPutDeduplicates(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	content := []byte("duplicate me")

	key1, size1, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	key2, size2, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	assert.Equal(t, key2, key1)

	assert.Equal(t, size2, size1)

}

func TestGetReturnsStoredContent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	content := []byte("retrieve me")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	rc, size, err := fs.Get(context.Background(), key)
	require.Nil(t, err)

	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.Nil(t, err)

	assert.True(t, bytes.Equal(got, content))

	assert.Equal(t, int64(len(content)), size)

}

func TestGetInvalidKeyReturnsErrNotExist(t *testing.T) {
	store, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	_, _, err = store.Get(context.Background(), "not-a-valid-key")
	assert.Equal(t, os.ErrNotExist, err)
}

func TestGetMissingKeyReturnsErrNotExist(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	// Use a valid-looking hex key so the shard path is well-formed.
	fakeKey := "aa" + hex.EncodeToString(make([]byte, 31))

	_, _, err = fs.Get(context.Background(), fakeKey)
	assert.Equal(t, os.ErrNotExist, err)

}

func TestDeleteRemovesContent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	content := []byte("delete me")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	require.NoError(t, fs.Delete(context.Background(), key))

	_, _, err = fs.Get(context.Background(), key)
	assert.Equal(t, os.ErrNotExist, err)

}

func TestGetReadsLegacyUncompressedBlob(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir, true)
	require.Nil(t, err)

	content := []byte("legacy uncompressed content that was stored before compression was added")
	h := sha256.Sum256(content)
	key := hex.EncodeToString(h[:])

	require.Nil(t, os.MkdirAll(filepath.Join(dir, key[:2]), 0o755))
	require.Nil(t, os.WriteFile(filepath.Join(dir, key[:2], key), content, 0o644))

	rc, size, err := store.Get(context.Background(), key)
	require.Nil(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.Nil(t, err)
	assert.True(t, bytes.Equal(got, content))
	assert.Equal(t, int64(len(content)), size)
}

func TestGetTinyLegacyBlob(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir, true)
	require.Nil(t, err)

	content := []byte("ab")
	h := sha256.Sum256(content)
	key := hex.EncodeToString(h[:])

	require.Nil(t, os.MkdirAll(filepath.Join(dir, key[:2]), 0o755))
	require.Nil(t, os.WriteFile(filepath.Join(dir, key[:2], key), content, 0o644))

	rc, size, err := store.Get(context.Background(), key)
	require.Nil(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.Nil(t, err)
	assert.True(t, bytes.Equal(got, content))
	assert.Equal(t, int64(2), size)
}

func TestPutNoCompressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir, false)
	require.Nil(t, err)

	content := bytes.Repeat([]byte("compressible data "), 1000)
	key, size, err := store.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)
	assert.Equal(t, int64(len(content)), size)

	diskPath := filepath.Join(dir, key[:2], key)
	info, err := os.Stat(diskPath)
	require.Nil(t, err)
	assert.Equal(t, int64(len(content)), info.Size())

	rc, gotSize, err := store.Get(context.Background(), key)
	require.Nil(t, err)
	defer rc.Close()
	assert.Equal(t, int64(len(content)), gotSize)
	got, err := io.ReadAll(rc)
	require.Nil(t, err)
	assert.True(t, bytes.Equal(got, content))
}

func TestPutCompressesOnDisk(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFilesystem(dir, true)
	require.Nil(t, err)

	content := bytes.Repeat([]byte("compressible data "), 1000)
	key, size, err := store.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)
	assert.Equal(t, int64(len(content)), size)

	diskPath := filepath.Join(dir, key[:2], key)
	info, err := os.Stat(diskPath)
	require.Nil(t, err)
	assert.Less(t, info.Size(), int64(len(content)))
}

func TestDeleteIdempotent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	fakeKey := "bb" + hex.EncodeToString(make([]byte, 31))

	assert.NoError(t, fs.Delete(context.Background(), fakeKey))

}

func TestExistsReturnsCorrectBoolean(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir(), true)
	require.Nil(t, err)

	fakeKey := "cc" + hex.EncodeToString(make([]byte, 31))

	exists, err := fs.Exists(context.Background(), fakeKey)
	require.Nil(t, err)

	assert.False(t, exists)

	content := []byte("exist check")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	exists, err = fs.Exists(context.Background(), key)
	require.Nil(t, err)

	assert.True(t, exists)

	// Verify Exists returns false after Delete.
	require.NoError(t, fs.Delete(context.Background(), key))

	exists, err = fs.Exists(context.Background(), key)
	require.Nil(t, err)

	assert.False(t, exists)

}
