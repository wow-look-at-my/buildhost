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
	repackagers map[Format]Repackager
}

func NewGenerator(store storage.Storage, database *db.DB, baseURL string) *Generator {
	rps := []Repackager{&TarGZ{}, &TarXZ{}, &TarZST{}, &Zip{}, &Deb{}, &Brew{}, &NPM{}, &OCI{Store: store, DB: database}}
	m := make(map[Format]Repackager, len(rps))
	for _, rp := range rps {
		m[rp.Format()] = rp
	}
	return &Generator{store: store, baseURL: baseURL, repackagers: m}
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

	data, err := io.ReadAll(rc)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "read artifact failed")
		return nil, fmt.Errorf("read artifact: %w", err)
	}

	if (artifact.Kind == model.KindBinary || artifact.Kind == model.KindLibrary) && strip.Available() {
		if result, err := strip.StripBytes(data); err == nil {
			data = result.Stripped
		}
	}

	out, err := rp.Repackage(ctx, Input{
		Project:  project,
		Release:  release,
		Artifact: artifact,
		Data:     data,
		BaseURL:  g.baseURL,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "repackage failed")
	}
	return out, err
}

func (g *Generator) Supports(format Format) bool {
	_, ok := g.repackagers[format]
	return ok
}
