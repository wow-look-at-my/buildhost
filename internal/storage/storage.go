package storage

import (
	"context"
	"errors"
	"io"
)

type Storage interface {
	Put(ctx context.Context, r io.Reader) (key string, size int64, err error)
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

// ErrCompressedUnsupported is returned by a CompressedGetter wrapper when the
// underlying backend cannot serve stored bytes without decompressing them.
var ErrCompressedUnsupported = errors.New("storage: compressed passthrough not supported by backend")

// CompressedBlob is a blob's stored bytes returned WITHOUT server-side
// decompression, plus the metadata a handler needs to serve them.
type CompressedBlob struct {
	// ReadCloser yields the on-the-wire bytes: the raw zstd stream when Encoding
	// is "zstd", or the identity bytes when Encoding is "".
	io.ReadCloser
	// Encoding is the HTTP Content-Encoding the bytes carry: "zstd" when the blob
	// is stored compressed, "" (identity) when stored raw.
	Encoding string
	// Size is the number of bytes ReadCloser yields (compressed length when
	// Encoding is "zstd", otherwise the identity length). It is the Content-Length.
	Size int64
	// OrigSize is the decompressed length of the artifact.
	OrigSize int64
}

// CompressedGetter is an optional Storage capability. GetCompressed returns a
// blob's stored bytes WITHOUT decompressing them, so a handler can pass a
// zstd-compressed blob straight through to a client that accepts zstd
// (Content-Encoding: zstd) and skip server-side decompression entirely. The
// returned Encoding tells the caller whether passthrough actually applies.
type CompressedGetter interface {
	GetCompressed(ctx context.Context, key string) (*CompressedBlob, error)
}
