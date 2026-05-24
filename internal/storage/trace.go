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
