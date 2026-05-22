package repackage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/storage"
	"github.com/wow-look-at-my/buildhost/internal/strip"
	"github.com/wow-look-at-my/go-mmap"
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
	Store       storage.Storage
	DB          *db.DB
	BaseURL     string
	TempDir     string
	Repackagers []Repackager
}

func NewOrchestrator(store storage.Storage, database *db.DB, baseURL, tempDir string) *Orchestrator {
	os.MkdirAll(tempDir, 0o755)
	return &Orchestrator{
		Store:   store,
		DB:      database,
		BaseURL: baseURL,
		TempDir: tempDir,
		Repackagers: []Repackager{
			&TarGZ{},
			&TarXZ{},
			&TarZST{},
			&Zip{},
			&Deb{},
			&Brew{},
			&NPM{},
		},
	}
}

func (o *Orchestrator) PublishRelease(ctx context.Context, project db.Project, release db.Release) error {
	artifacts, err := o.DB.ListArtifacts(ctx, release.ID)
	if err != nil {
		return fmt.Errorf("list artifacts: %w", err)
	}

	for i := range artifacts {
		a := &artifacts[i]

		if (a.Kind == db.KindBinary || a.Kind == db.KindLibrary) && strip.Available() {
			if err := o.stripArtifact(ctx, a); err != nil {
				slog.Warn("strip failed, using original", "artifact", a.ID, "err", err)
			}
		}

		for _, rp := range o.Repackagers {
			if !rp.Applicable(*a) {
				continue
			}

			key := a.StorageKey
			if a.StrippedStorageKey != "" && (a.Kind == db.KindBinary || a.Kind == db.KindLibrary) {
				key = a.StrippedStorageKey
			}

			rc, _, err := o.Store.Get(ctx, key)
			if err != nil {
				slog.Error("get artifact for repackaging", "format", rp.Format(), "err", err)
				continue
			}

			tmpFile, err := copyToTempFile(rc, o.TempDir, "repackage-*")
			rc.Close()
			if err != nil {
				slog.Error("copy artifact to temp", "err", err)
				continue
			}
			tmpFile.Close()

			m, err := mmap.MapFile(tmpFile.Name())
			if err != nil {
				os.Remove(tmpFile.Name())
				slog.Error("mmap artifact", "err", err)
				continue
			}

			input := Input{
				Project:  project,
				Release:  release,
				Artifact: *a,
				Data:     m,
				BaseURL:  o.BaseURL,
			}

			output, err := rp.Repackage(ctx, input)
			m.Unmap()
			os.Remove(tmpFile.Name())

			if err != nil {
				slog.Error("repackage failed", "format", rp.Format(), "artifact", a.ID, "err", err)
				continue
			}

			storageKey, size, err := o.Store.Put(ctx, output.Reader)
			if err != nil {
				slog.Error("store repackaged artifact", "format", rp.Format(), "err", err)
				continue
			}

			metadata := "{}"
			if output.Metadata != nil {
				metadata = marshalMetadata(output.Metadata)
			}

			if err := o.DB.CreatePackagedArtifact(ctx, a.ID, string(rp.Format()), storageKey, size, storageKey, output.Filename, metadata); err != nil {
				slog.Error("record packaged artifact", "format", rp.Format(), "err", err)
			}
		}
	}

	return o.DB.PublishRelease(ctx, release.ID)
}

func (o *Orchestrator) stripArtifact(ctx context.Context, a *db.Artifact) error {
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

func marshalMetadata(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}
