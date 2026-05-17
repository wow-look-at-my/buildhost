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
	Binary   io.ReadSeeker
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
	Store       storage.Storage
	DB          *db.DB
	BaseURL     string
	Repackagers []Repackager
}

func NewOrchestrator(store storage.Storage, database *db.DB, baseURL string) *Orchestrator {
	return &Orchestrator{
		Store:   store,
		DB:      database,
		BaseURL: baseURL,
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

		for _, rp := range o.Repackagers {
			if !rp.Applicable(*a) {
				continue
			}

			key := a.StorageKey
			if a.StrippedStorageKey != "" && (a.Kind == model.KindBinary || a.Kind == model.KindLibrary) {
				key = a.StrippedStorageKey
			}

			rc, _, err := o.Store.Get(ctx, key)
			if err != nil {
				slog.Error("get artifact for repackaging", "format", rp.Format(), "err", err)
				continue
			}

			tmpFile, err := os.CreateTemp("", "repackage-*")
			if err != nil {
				rc.Close()
				slog.Error("create temp for repackaging", "err", err)
				continue
			}
			io.Copy(tmpFile, rc)
			rc.Close()
			tmpFile.Seek(0, io.SeekStart)

			input := Input{
				Project:  project,
				Release:  release,
				Artifact: *a,
				Binary:   tmpFile,
				BaseURL:  o.BaseURL,
			}

			output, err := rp.Repackage(ctx, input)
			tmpFile.Close()
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

func (o *Orchestrator) stripArtifact(ctx context.Context, a *model.Artifact) error {
	rc, _, err := o.Store.Get(ctx, a.StorageKey)
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp("", "strip-*")
	if err != nil {
		rc.Close()
		return err
	}
	io.Copy(tmpFile, rc)
	rc.Close()
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

func marshalMetadata(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	s := "{"
	first := true
	for k, v := range m {
		if !first {
			s += ","
		}
		s += `"` + k + `":"` + v + `"`
		first = false
	}
	s += "}"
	return s
}
