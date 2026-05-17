package storage

import (
	"context"
	"io"
)

type Storage interface {
	Put(ctx context.Context, r io.Reader) (key string, size int64, err error)
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}
