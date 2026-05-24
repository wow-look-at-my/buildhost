package storage_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

type memStorage struct {
	data map[string][]byte
}

func newMemStorage() *memStorage {
	return &memStorage{data: make(map[string][]byte)}
}

func (m *memStorage) Put(_ context.Context, r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	key := "abc123"
	m.data[key] = b
	return key, int64(len(b)), nil
}

func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	b, ok := m.data[key]
	if !ok {
		return nil, 0, io.EOF
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}

func (m *memStorage) Delete(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *memStorage) Exists(_ context.Context, key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

func TestTracedStorage(t *testing.T) {
	inner := newMemStorage()
	s := storage.NewTraced(inner)
	ctx := context.Background()

	key, size, err := s.Put(ctx, bytes.NewReader([]byte("hello")))
	require.NoError(t, err)
	assert.Equal(t, "abc123", key)
	assert.Equal(t, int64(5), size)

	exists, err := s.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)

	rc, sz, err := s.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, int64(5), sz)
	data, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, []byte("hello"), data)

	err = s.Delete(ctx, key)
	require.NoError(t, err)

	exists, err = s.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestTracedStorage_GetMissing(t *testing.T) {
	inner := newMemStorage()
	s := storage.NewTraced(inner)

	_, _, err := s.Get(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestTracedStorage_DeleteMissing(t *testing.T) {
	inner := newMemStorage()
	s := storage.NewTraced(inner)

	err := s.Delete(context.Background(), "nonexistent")
	assert.NoError(t, err)
}

func TestTracedStorage_ExistsMissing(t *testing.T) {
	inner := newMemStorage()
	s := storage.NewTraced(inner)

	exists, err := s.Exists(context.Background(), "nonexistent")
	require.NoError(t, err)
	assert.False(t, exists)
}
