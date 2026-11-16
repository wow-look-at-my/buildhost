package storage

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetCompressChangesHowNewBlobsAreStored(t *testing.T) {
	t.Serial()
	fs, err := NewFilesystem(t.TempDir(), true)
	require.NoError(t, err)
	ctx := context.Background()
	encoding := func(key string) string {
		b, err := fs.GetCompressed(ctx, key)
		require.NoError(t, err)
		defer b.Close()
		return b.Encoding
	}
	read := func(key string) string {
		r, _, err := fs.Get(ctx, key)
		require.NoError(t, err)
		defer r.Close()
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		return string(data)
	}
	first, second := string(bytes.Repeat([]byte("first "), 400)), string(bytes.Repeat([]byte("second "), 400))

	zKey, _, err := fs.Put(ctx, bytes.NewReader([]byte(first)))
	require.NoError(t, err)
	assert.Equal(t, "zstd", encoding(zKey))

	fs.SetCompress(false)
	rawKey, _, err := fs.Put(ctx, bytes.NewReader([]byte(second)))
	require.NoError(t, err)
	assert.Empty(t, encoding(rawKey), "a blob stored after the change is not compressed")
	assert.Equal(t, first, read(zKey), "a blob stored before the change stays readable")
	assert.Equal(t, second, read(rawKey))

	fs.SetCompress(true)
	assert.Equal(t, second, read(rawKey))
}
