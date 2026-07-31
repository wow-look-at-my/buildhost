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

// ErrRandomUnsupported is returned by a RandomGetter when a blob cannot be read
// at an offset -- because the backend cannot, or because that blob is stored
// zstd-compressed, which has no offsets to seek to.
var ErrRandomUnsupported = errors.New("storage: random access not supported for this blob")

// ReaderAtCloser is a random-access view of a blob's bytes. It is safe for
// concurrent use (an mmap'd blob is), and Close releases the mapping.
type ReaderAtCloser interface {
	io.ReaderAt
	io.Closer
}

// RandomGetter is an optional Storage capability: read a blob at an offset
// instead of streaming it from the start. It is what makes an indexed
// container (see internal/binarchive) worth storing -- an index into bytes you
// can only read sequentially saves nothing.
//
// Only a blob stored UNCOMPRESSED can be read this way; anything else returns
// ErrRandomUnsupported and the caller falls back to Get. Put compresses by
// default, so a blob meant for random access is written with
// UncompressedPutter.
type RandomGetter interface {
	OpenReaderAt(ctx context.Context, key string) (ReaderAtCloser, int64, error)
}

// UncompressedPutter is an optional Storage capability: store a blob without
// the storage layer's own zstd wrapper, so it stays randomly accessible. Use
// it for content that carries its own compression (a binpazer archive
// compresses each block on its own); double-compressing it would only trade
// the index away for nothing.
type UncompressedPutter interface {
	PutUncompressed(ctx context.Context, r io.Reader) (key string, size int64, err error)
}
