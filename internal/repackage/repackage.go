package repackage

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
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
	Project  model.Project
	Release  model.Release
	Artifact model.Artifact
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
	Applicable(artifact model.Artifact) bool
	Repackage(ctx context.Context, input Input) (*Output, error)
}

type Orchestrator struct {
	Store   storage.Storage
	DB      *db.DB
	TempDir string
}

func NewOrchestrator(store storage.Storage, database *db.DB, tempDir string) *Orchestrator {
	os.MkdirAll(tempDir, 0o755)
	return &Orchestrator{
		Store:   store,
		DB:      database,
		TempDir: tempDir,
	}
}

func (o *Orchestrator) PublishRelease(ctx context.Context, project model.Project, release model.Release) error {
	artifacts, err := o.DB.ListArtifacts(ctx, release.ID)
	if err != nil {
		return fmt.Errorf("list artifacts: %w", err)
	}

	for i := range artifacts {
		a := &artifacts[i]

		if (a.Kind == model.KindBinary || a.Kind == model.KindLibrary) && strip.Available() {
			if err := o.stripArtifact(ctx, a); err != nil {
				slog.Warn("strip failed, using original", "artifact", a.ID, "err", err)
			}
		}
	}

	return o.DB.PublishRelease(ctx, release.ID)
}

func (o *Orchestrator) stripArtifact(ctx context.Context, a *model.Artifact) error {
	rc, _, err := o.Store.Get(ctx, a.StorageKey)
	if err != nil {
		return err
	}

	tmpFile, err := copyToTempFile(rc, o.TempDir, "strip-*")
	rc.Close()
	if err != nil {
		return fmt.Errorf("copy artifact to temp: %w", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	result, err := strip.Strip(tmpFile.Name())
	if err != nil {
		return err
	}
	defer os.Remove(result.StrippedPath)
	defer os.Remove(result.DebugPath)

	strippedFile, err := os.Open(result.StrippedPath)
	if err != nil {
		return err
	}
	defer strippedFile.Close()
	strippedKey, strippedSize, err := o.Store.Put(ctx, strippedFile)
	if err != nil {
		return err
	}

	debugFile, err := os.Open(result.DebugPath)
	if err != nil {
		return err
	}
	defer debugFile.Close()
	debugKey, debugSize, err := o.Store.Put(ctx, debugFile)
	if err != nil {
		return err
	}

	a.StrippedStorageKey = strippedKey
	a.StrippedSize = strippedSize
	a.StrippedSHA256 = strippedKey
	a.DebugStorageKey = debugKey
	a.DebugSize = debugSize

	return o.DB.UpdateArtifactStripped(ctx, a.ID, strippedKey, strippedSize, strippedKey, debugKey, debugSize)
}

func copyToTempFile(r io.Reader, dir, prefix string) (*os.File, error) {
	f, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return f, nil
}

