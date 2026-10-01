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
var ErrCompressedUnsupported = errors.New("storage: compressed passthrough not supported by backend")

// CompressedBlob is a blob's stored bytes returned WITHOUT server-side
// decompression, plus the metadata a handler needs to serve them.
type CompressedBlob struct {
	// ReadCloser yields the on-the-wire bytes: the raw zstd stream when Encoding
	io.ReadCloser
	// Encoding is the HTTP Content-Encoding the bytes carry: "zstd" when the blob
	Encoding string
	// Size is the number of bytes ReadCloser yields (compressed length when
	Size int64
	// OrigSize is the decompressed length of the artifact.
	OrigSize int64
}

// CompressedGetter is an optional Storage capability. GetCompressed returns a
type CompressedGetter interface {
	GetCompressed(ctx context.Context, key string) (*CompressedBlob, error)
}

// ErrRandomUnsupported is returned by a RandomGetter when a blob cannot be read
var ErrRandomUnsupported = errors.New("storage: random access not supported for this blob")

// ReaderAtCloser is a random-access view of a blob's bytes. It is safe for
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}

// RandomGetter is an optional Storage capability: read a blob at an offset
// instead of streaming it from the start. It is what makes an indexed
type RandomGetter interface {
	OpenReaderAt(ctx context.Context, key string) (ReaderAtCloser, int64, error)
}

// UncompressedPutter is an optional Storage capability: store a blob without
type UncompressedPutter interface {
	PutUncompressed(ctx context.Context, r io.Reader) (key string, size int64, err error)
}
