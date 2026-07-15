package repackage

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/serviceurl"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
)

var repackTracer = otel.Tracer("buildhost.repackage")

// dlServiceURL constructs the dl subdomain URL from the root domain base URL
// (e.g. "https://pazer.build" → "https://dl.pazer.build"), or "" if baseURL is
// not a usable scheme://host.
func dlServiceURL(baseURL string) string {
	u, err := serviceurl.Base(baseURL, "dl")
	if err != nil {
		return ""
	}
	return u
}

type Generator struct {
	store       storage.Storage
	tmpDir      string
	repackagers map[Format]Repackager
}

func NewGenerator(store storage.Storage, database *db.DB, tmpDir string) *Generator {
	m := make(map[Format]Repackager, len(registry)+1)
	for f, rp := range registry {
		m[f] = rp
	}
	oci := &OCI{Store: store, DB: database}
	m[oci.Format()] = oci
	return &Generator{store: store, tmpDir: tmpDir, repackagers: m}
}

// Generate repackages an artifact into format. baseURL is this server's own base
// URL (derived per-request from the Host), used to build absolute download/home
// URLs in formats like brew.
func (g *Generator) Generate(ctx context.Context, format Format, project db.Project, release db.Release, artifact db.Artifact, baseURL string) (*Output, error) {
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

	reader, size, err := OpenArtifactStream(ctx, g.store, artifact, g.tmpDir)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "open artifact failed")
		return nil, fmt.Errorf("open artifact: %w", err)
	}

	_, convertSpan := repackTracer.Start(ctx, "repackage.convert")
	convertSpan.SetAttributes(
		attribute.String("repackage.format", string(format)),
		attribute.Int64("repackage.input_bytes", size),
	)
	dlBase := dlServiceURL(baseURL)
	out, err := rp.Repackage(ctx, Input{
		Project:  project,
		Release:  release,
		Artifact: artifact,
		Reader:   reader,
		Size:     size,
		TmpDir:   g.tmpDir,
		BaseURL:  baseURL,
		DownloadURL: func(name, version string, os db.OS, arch db.Arch, format string) string {
			q := url.Values{}
			q.Set("os", string(os))
			q.Set("arch", string(arch))
			if version != "" {
				q.Set("v", version)
			}
			if format != "" && format != "raw" {
				q.Set("fmt", format)
			}
			return dlBase + "/" + name + "?" + q.Encode()
		},
	})
	if err != nil {
		reader.Close()
		convertSpan.RecordError(err)
		convertSpan.SetStatus(codes.Error, "repackage failed")
		convertSpan.End()
		span.RecordError(err)
		span.SetStatus(codes.Error, "repackage failed")
		return nil, err
	}
	// The repackager reads the input stream lazily (its output is a pipe), so the input
	// must stay open until the caller finishes reading the output. Tie its Close to the
	// output's Close.
	out.Reader = ChainClose(out.Reader, reader)
	if out.Size >= 0 {
		convertSpan.SetAttributes(attribute.Int64("repackage.output_bytes", out.Size))
	}
	convertSpan.End()
	return out, nil
}

// OpenArtifactStream opens an artifact's bytes as a stream for repackaging. When
// stripping is available and the artifact is a binary/library it returns the stripped
// stream and the stripped size; otherwise the raw stored stream and its size. Reader and
// size always agree, so a tar/ar/npm header written from the size matches the body. The
// caller MUST Close the returned reader.
func OpenArtifactStream(ctx context.Context, store storage.Storage, artifact db.Artifact, tmpDir string) (io.ReadCloser, int64, error) {
	rc, size, err := store.Get(ctx, artifact.StorageKey)
	if err != nil {
		return nil, 0, err
	}
	if (artifact.Kind == db.KindBinary || artifact.Kind == db.KindLibrary) && strip.Available() {
		sr, ssize, serr := strip.StripReader(rc, tmpDir)
		rc.Close()
		if serr == nil {
			return sr, ssize, nil
		}
		// Strip failed (e.g. not an ELF): the first reader was consumed, so re-open the
		// raw artifact and serve it unstripped.
		rc, size, err = store.Get(ctx, artifact.StorageKey)
		if err != nil {
			return nil, 0, err
		}
	}
	return rc, size, nil
}

func (g *Generator) Supports(format Format) bool {
	_, ok := g.repackagers[format]
	return ok
}
