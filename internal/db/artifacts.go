package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) CreateArtifact(ctx context.Context, a *Artifact) error {
	return d.CreateArtifacts(ctx, []*Artifact{a})
}

func (d *DB) CreateMultiPlatformArtifact(ctx context.Context, a *Artifact, platforms []Platform) error {
	if len(platforms) == 0 {
		return errors.New("create multi-platform artifact: no platforms")
	}
	a.OS, a.Arch = platforms[0].OS, platforms[0].Arch
	return d.createArtifacts(ctx, []*Artifact{a}, [][]Platform{platforms})
}

func (d *DB) CreateArtifacts(ctx context.Context, artifacts []*Artifact) error {
	sets := make([][]Platform, len(artifacts))
	for i, a := range artifacts {
		sets[i] = []Platform{{OS: a.OS, Arch: a.Arch}}
	}
	return d.createArtifacts(ctx, artifacts, sets)
}

func (d *DB) createArtifacts(ctx context.Context, artifacts []*Artifact, sets [][]Platform) error {
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	q := New(tx)
	for i, a := range artifacts {
		res, err := q.InsertArtifact(ctx, InsertArtifactParams{
			ReleaseID:  a.ReleaseID,
			OS:         a.OS,
			Arch:       a.Arch,
			Kind:       a.Kind,
			StorageKey: a.StorageKey,
			Size:       a.Size,
			SHA256:     a.SHA256,
			Filename:   a.Filename,
			ExeFormat:  a.ExeFormat,
		})
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("artifact %s/%s: %w", a.OS, a.Arch, ErrConflict)
			}
			return fmt.Errorf("insert artifact: %w", err)
		}
		id, _ := res.LastInsertId()
		a.ID = id

		for ordinal, p := range sets[i] {
			err := q.InsertArtifactPlatform(ctx, InsertArtifactPlatformParams{
				ArtifactID: id,
				ReleaseID:  a.ReleaseID,
				Kind:       a.Kind,
				OS:         p.OS,
				Arch:       p.Arch,
				Ordinal:    int64(ordinal),
			})
			if err != nil {
				if isUniqueViolation(err) {
					return fmt.Errorf("artifact %s: %w", p, ErrConflict)
				}
				return fmt.Errorf("insert artifact platform: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ArtifactPlatforms returns every (os, arch) slot an artifact occupies, in the
func (d *DB) ArtifactPlatforms(ctx context.Context, artifactID int64) ([]Platform, error) {
	rows, err := d.q.ListArtifactPlatformsByArtifact(ctx, artifactID)
	if err != nil {
		return nil, fmt.Errorf("list artifact platforms: %w", err)
	}
	out := make([]Platform, len(rows))
	for i, r := range rows {
		out[i] = Platform{OS: r.OS, Arch: r.Arch}
	}
	return out, nil
}

func (d *DB) ListArtifactsWithPlatforms(ctx context.Context, releaseID int64) ([]ArtifactWithPlatforms, error) {
	arts, err := d.ListArtifacts(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	rows, err := d.q.ListArtifactPlatformsByRelease(ctx, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list release platforms: %w", err)
	}
	byArtifact := make(map[int64][]Platform, len(arts))
	for _, r := range rows {
		byArtifact[r.ArtifactID] = append(byArtifact[r.ArtifactID], Platform{OS: r.OS, Arch: r.Arch})
	}
	out := make([]ArtifactWithPlatforms, len(arts))
	for i, a := range arts {
		out[i] = ArtifactWithPlatforms{Artifact: a, Platforms: byArtifact[a.ID]}
	}
	return out, nil
}

func (d *DB) UpdateArtifactStripped(ctx context.Context, id int64, strippedKey string, strippedSize int64, strippedSHA256 string, debugKey string, debugSize int64) error {
	return d.q.UpdateArtifactStripped(ctx, UpdateArtifactStrippedParams{
		ID:                 id,
		StrippedStorageKey: strippedKey,
		StrippedSize:       strippedSize,
		StrippedSHA256:     strippedSHA256,
		DebugStorageKey:    debugKey,
		DebugSize:          debugSize,
	})
}

func (d *DB) GetArtifactByKind(ctx context.Context, releaseID int64, kind Kind) (*Artifact, error) {
	row, err := d.q.GetArtifactByReleaseAndKind(ctx, GetArtifactByReleaseAndKindParams{
		ReleaseID: releaseID,
		Kind:      kind,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact by kind: %w", err)
	}
	return &row, nil
}

func (d *DB) GetArtifact(ctx context.Context, releaseID int64, os, arch string) (*Artifact, error) {
	row, err := d.q.GetArtifactByReleaseOSArch(ctx, GetArtifactByReleaseOSArchParams{
		ReleaseID: releaseID,
		OS:        OS(os),
		Arch:      Arch(arch),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return &row, nil
}

func (d *DB) ListArtifactsByPlatform(ctx context.Context, releaseID int64) ([]PlatformArtifact, error) {
	arts, err := d.ListArtifactsWithPlatforms(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	var out []PlatformArtifact
	for _, a := range arts {
		for _, p := range a.Platforms {
			out = append(out, newPlatformArtifact(a.Artifact, p))
		}
	}
	return out, nil
}

func (d *DB) GetPlatformArtifact(ctx context.Context, releaseID int64, os, arch string) (*PlatformArtifact, error) {
	a, err := d.GetArtifact(ctx, releaseID, os, arch)
	if err != nil {
		return nil, err
	}
	pa := newPlatformArtifact(*a, Platform{OS: OS(os), Arch: Arch(arch)})
	return &pa, nil
}

// CanonicalPlatform maps a requested (os, arch) to the canonical slot of the
// artifact covering it. For an ordinary per-platform artifact that is the same
func (d *DB) CanonicalPlatform(ctx context.Context, releaseID int64, os, arch string) (string, string, error) {
	a, err := d.GetArtifact(ctx, releaseID, os, arch)
	if err != nil {
		return "", "", err
	}
	return string(a.OS), string(a.Arch), nil
}

func (d *DB) ListArtifacts(ctx context.Context, releaseID int64) ([]Artifact, error) {
	return d.q.ListArtifactsByRelease(ctx, releaseID)
}

func (d *DB) CreatePackagedArtifact(ctx context.Context, artifactID int64, format, storageKey string, size int64, sha256, filename, metadata string) error {
	return d.q.UpsertPackagedArtifact(ctx, UpsertPackagedArtifactParams{
		ArtifactID: artifactID,
		Format:     format,
		StorageKey: storageKey,
		Size:       size,
		SHA256:     sha256,
		Filename:   filename,
		Metadata:   metadata,
	})
}

func (d *DB) GetPackagedArtifact(ctx context.Context, artifactID int64, format string) (storageKey string, size int64, sha256sum string, filename string, metadata string, err error) {
	row, err := d.q.GetPackagedArtifact(ctx, GetPackagedArtifactParams{
		ArtifactID: artifactID,
		Format:     format,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", "", "", ErrNotFound
	}
	if err != nil {
		return "", 0, "", "", "", err
	}
	return row.StorageKey, row.Size, row.SHA256, row.Filename, row.Metadata, nil
}

func (d *DB) BlobBelongsToProject(ctx context.Context, projectID int64, storageKey string) (bool, error) {
	exists, err := d.q.BlobBelongsToProject(ctx, BlobBelongsToProjectParams{
		ProjectID:  projectID,
		StorageKey: storageKey,
	})
	return exists != 0, err
}
