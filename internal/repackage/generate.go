package repackage

import (
	"context"
	"fmt"
	"io"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
)

var repackTracer = otel.Tracer("buildhost.repackage")

type Generator struct {
	store       storage.Storage
	baseURL     string
	tmpDir      string
	repackagers map[Format]Repackager
}

func NewGenerator(store storage.Storage, database *db.DB, baseURL, tmpDir string) *Generator {
	m := make(map[Format]Repackager, len(registry)+1)
	for f, rp := range registry {
		m[f] = rp
	}
	oci := &OCI{Store: store, DB: database}
	m[oci.Format()] = oci
	return &Generator{store: store, baseURL: baseURL, tmpDir: tmpDir, repackagers: m}
}

func (g *Generator) Generate(ctx context.Context, format Format, project model.Project, release model.Release, artifact model.Artifact) (*Output, error) {
	ctx, span := repackTracer.Start(ctx, "repackage.generate")
	defer span.End()
	span.SetAttributes(
		attribute.String("repackage.format", string(format)),
		attribute.String("repackage.project", project.Name),
		attribute.String("repackage.version", release.Version),
		attribute.String("repackage.os", string(artifact.OS)),
		attribute.String("repackage.arch", string(artifact.Arch)),
	)

	rp, ok := g.repackagers[format]
	if !ok {
		err := fmt.Errorf("unsupported format: %s", format)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	rc, _, err := g.store.Get(ctx, artifact.StorageKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get artifact failed")
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	defer rc.Close()

	_, readSpan := repackTracer.Start(ctx, "repackage.read_blob")
	data, err := io.ReadAll(rc)
	readSpan.SetAttributes(attribute.Int("repackage.blob_bytes", len(data)))
	if err != nil {
		readSpan.RecordError(err)
		readSpan.SetStatus(codes.Error, "read artifact failed")
		readSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, "read artifact failed")
		return nil, fmt.Errorf("read artifact: %w", err)
	}
	readSpan.End()

	if (artifact.Kind == model.KindBinary || artifact.Kind == model.KindLibrary) && strip.Available() {
		_, stripSpan := repackTracer.Start(ctx, "repackage.strip")
		stripSpan.SetAttributes(attribute.Int("strip.input_bytes", len(data)))
		if result, err := strip.StripBytes(data, g.tmpDir); err == nil {
			stripSpan.SetAttributes(attribute.Int("strip.output_bytes", len(result.Stripped)))
			data = result.Stripped
		} else {
			stripSpan.SetAttributes(attribute.String("strip.skip_reason", err.Error()))
		}
		stripSpan.End()
	}

	_, convertSpan := repackTracer.Start(ctx, "repackage.convert")
	convertSpan.SetAttributes(
		attribute.String("repackage.format", string(format)),
		attribute.Int("repackage.input_bytes", len(data)),
	)
	out, err := rp.Repackage(ctx, Input{
		Project:  project,
		Release:  release,
		Artifact: artifact,
		Data:     data,
		BaseURL:  g.baseURL,
	})
	if err != nil {
		convertSpan.RecordError(err)
		convertSpan.SetStatus(codes.Error, "repackage failed")
		convertSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, "repackage failed")
		return nil, err
	}
	convertSpan.SetAttributes(attribute.Int64("repackage.output_bytes", out.Size))
	convertSpan.End()
	return out, nil
}

func (g *Generator) Supports(format Format) bool {
	_, ok := g.repackagers[format]
	return ok
}
