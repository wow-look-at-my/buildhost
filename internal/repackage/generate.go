package repackage

import (
	"context"
	"fmt"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Generator struct {
	store       storage.Storage
	baseURL     string
	repackagers map[Format]Repackager
}

func NewGenerator(store storage.Storage, baseURL string) *Generator {
	rps := []Repackager{&TarGZ{}, &TarXZ{}, &TarZST{}, &Zip{}, &Deb{}, &Brew{}, &NPM{}, &OCI{}}
	m := make(map[Format]Repackager, len(rps))
	for _, rp := range rps {
		m[rp.Format()] = rp
	}
	return &Generator{store: store, baseURL: baseURL, repackagers: m}
}

func (g *Generator) Generate(ctx context.Context, format Format, project model.Project, release model.Release, artifact model.Artifact) (*Output, error) {
	rp, ok := g.repackagers[format]
	if !ok {
		return nil, fmt.Errorf("unsupported format: %s", format)
	}

	key := artifact.StorageKey
	if artifact.StrippedStorageKey != "" && (artifact.Kind == model.KindBinary || artifact.Kind == model.KindLibrary) {
		key = artifact.StrippedStorageKey
	}

	rc, _, err := g.store.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read artifact: %w", err)
	}

	return rp.Repackage(ctx, Input{
		Project:  project,
		Release:  release,
		Artifact: artifact,
		Data:     data,
		BaseURL:  g.baseURL,
	})
}

func (g *Generator) Supports(format Format) bool {
	_, ok := g.repackagers[format]
	return ok
}
