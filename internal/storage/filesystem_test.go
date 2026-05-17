package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestPutStoresContentAndReturnsCorrectKey(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
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
	fs, err := NewFilesystem(t.TempDir())
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
	fs, err := NewFilesystem(t.TempDir())
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

func TestGetMissingKeyReturnsErrNotExist(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	require.Nil(t, err)

	// Use a valid-looking hex key so the shard path is well-formed.
	fakeKey := "aa" + hex.EncodeToString(make([]byte, 31))

	_, _, err = fs.Get(context.Background(), fakeKey)
	assert.Equal(t, os.ErrNotExist, err)

}

func TestDeleteRemovesContent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	require.Nil(t, err)

	content := []byte("delete me")
	key, _, err := fs.Put(context.Background(), bytes.NewReader(content))
	require.Nil(t, err)

	require.NoError(t, fs.Delete(context.Background(), key))

	_, _, err = fs.Get(context.Background(), key)
	assert.Equal(t, os.ErrNotExist, err)

}

func TestDeleteIdempotent(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
	require.Nil(t, err)

	fakeKey := "bb" + hex.EncodeToString(make([]byte, 31))

	assert.NoError(t, fs.Delete(context.Background(), fakeKey))

}

func TestExistsReturnsCorrectBoolean(t *testing.T) {
	fs, err := NewFilesystem(t.TempDir())
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
