package repackage

import (
	"context"
	"io"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

type Format string

const (
	FormatTarGZ Format = "tar.gz"
	FormatTarXZ Format = "tar.xz"
	FormatTarZST Format = "tar.zst"
	FormatZip   Format = "zip"
	FormatDeb   Format = "deb"
	FormatBrew  Format = "brew"
	FormatNPM   Format = "npm"
	FormatOCI   Format = "oci"
)

type Input struct {
	Project  db.Project
	Release  db.Release
	Artifact db.Artifact
	Data     []byte
	BaseURL  string
}

type Output struct {
	Reader   io.Reader
	Filename string
	Size     int64
	Metadata map[string]string
}

type Repackager interface {
	Format() Format
	Applicable(artifact db.Artifact) bool
	Repackage(ctx context.Context, input Input) (*Output, error)
}

type Orchestrator struct {
	Store storage.Storage
	DB    *db.DB
}

func NewOrchestrator(store storage.Storage, database *db.DB) *Orchestrator {
	return &Orchestrator{Store: store, DB: database}
}

func (o *Orchestrator) PublishRelease(ctx context.Context, _ db.Project, release db.Release) error {
	return o.DB.PublishRelease(ctx, release.ID)
}

