package storage

import (
	"context"
	"io"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var storageTracer = otel.Tracer("buildhost.storage")

type TracedStorage struct {
	inner Storage
}

func NewTraced(s Storage) Storage {
	return &TracedStorage{inner: s}
}

func (t *TracedStorage) Put(ctx context.Context, r io.Reader) (string, int64, error) {
	ctx, span := storageTracer.Start(ctx, "storage.put")
	defer span.End()

	key, size, err := t.inner.Put(ctx, r)
	span.SetAttributes(
		attribute.String("storage.key", key),
		attribute.Int64("storage.size", size),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return key, size, err
}

func (t *TracedStorage) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	ctx, span := storageTracer.Start(ctx, "storage.get",
		trace.WithAttributes(attribute.String("storage.key", key)),
	)
	defer span.End()

	rc, size, err := t.inner.Get(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(attribute.Int64("storage.size", size))
	}
	return rc, size, err
}

// PutUncompressed forwards to the inner backend's UncompressedPutter
// capability, falling back to the ordinary Put when it has none (the blob is
// then stored compressed and simply is not randomly accessible).
func (t *TracedStorage) PutUncompressed(ctx context.Context, r io.Reader) (string, int64, error) {
	up, ok := t.inner.(UncompressedPutter)
	if !ok {
		return t.Put(ctx, r)
	}
	ctx, span := storageTracer.Start(ctx, "storage.put_uncompressed")
	defer span.End()

	key, size, err := up.PutUncompressed(ctx, r)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(attribute.String("storage.key", key), attribute.Int64("storage.size", size))
	}
	return key, size, err
}

// OpenReaderAt forwards to the inner backend's RandomGetter capability. It
// returns ErrRandomUnsupported when the backend has none, so callers fall back
// to a sequential Get.
func (t *TracedStorage) OpenReaderAt(ctx context.Context, key string) (ReaderAtCloser, int64, error) {
	rg, ok := t.inner.(RandomGetter)
	if !ok {
		return nil, 0, ErrRandomUnsupported
	}
	ctx, span := storageTracer.Start(ctx, "storage.open_reader_at",
		trace.WithAttributes(attribute.String("storage.key", key)),
	)
	defer span.End()

	ra, size, err := rg.OpenReaderAt(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(attribute.Int64("storage.size", size))
	}
	return ra, size, err
}

// GetCompressed forwards to the inner backend's CompressedGetter capability (the
// zstd-passthrough read path). It returns ErrCompressedUnsupported when the inner
// backend does not implement it, so callers transparently fall back to Get.
func (t *TracedStorage) GetCompressed(ctx context.Context, key string) (*CompressedBlob, error) {
	cg, ok := t.inner.(CompressedGetter)
	if !ok {
		return nil, ErrCompressedUnsupported
	}

	ctx, span := storageTracer.Start(ctx, "storage.get_compressed",
		trace.WithAttributes(attribute.String("storage.key", key)),
	)
	defer span.End()

	blob, err := cg.GetCompressed(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(
			attribute.Int64("storage.size", blob.Size),
			attribute.String("storage.encoding", blob.Encoding),
		)
	}
	return blob, err
}

func (t *TracedStorage) Delete(ctx context.Context, key string) error {
	ctx, span := storageTracer.Start(ctx, "storage.delete",
		trace.WithAttributes(attribute.String("storage.key", key)),
	)
	defer span.End()

	err := t.inner.Delete(ctx, key)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

func (t *TracedStorage) Exists(ctx context.Context, key string) (bool, error) {
	ctx, span := storageTracer.Start(ctx, "storage.exists",
		trace.WithAttributes(attribute.String("storage.key", key)),
	)
	defer span.End()

	exists, err := t.inner.Exists(ctx, key)
	span.SetAttributes(attribute.Bool("storage.found", exists))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return exists, err
}
