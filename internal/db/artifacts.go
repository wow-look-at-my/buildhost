package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CreateArtifact inserts one artifact occupying exactly the (os, arch) slot on
// the row. Use CreateMultiPlatformArtifact for one file covering several.
func (d *DB) CreateArtifact(ctx context.Context, a *Artifact) error {
	return d.CreateArtifacts(ctx, []*Artifact{a})
}

// CreateMultiPlatformArtifact inserts ONE artifact row covering every platform
// in the set: one blob, one row, one download link, N occupied slots. platforms
// must be non-empty; platforms[0] becomes the row's canonical slot (its
// os/arch), which is what every covered platform's download redirect folds to.
//
// Slot conflicts are reported as ErrConflict naming the platform, exactly like
// a single-platform create, and nothing is written -- the whole set lands or
// none of it does.
func (d *DB) CreateMultiPlatformArtifact(ctx context.Context, a *Artifact, platforms []Platform) error {
	if len(platforms) == 0 {
		return errors.New("create multi-platform artifact: no platforms")
	}
	a.OS, a.Arch = platforms[0].OS, platforms[0].Arch
	return d.createArtifacts(ctx, []*Artifact{a}, [][]Platform{platforms})
}

// CreateArtifacts inserts several artifact rows in one transaction: either
// every row is created or none is. It backs the multi-platform upload fan-out
// (one uploaded blob published for several os/arch combinations), where a
// conflict on any single combination must leave the release unchanged so the
// client can resolve it and retry the identical request. A unique violation is
// reported as ErrConflict naming the conflicting combination. On success each
// artifact's ID is filled in.
func (d *DB) CreateArtifacts(ctx context.Context, artifacts []*Artifact) error {
	sets := make([][]Platform, len(artifacts))
	for i, a := range artifacts {
		sets[i] = []Platform{{OS: a.OS, Arch: a.Arch}}
	}
	return d.createArtifacts(ctx, artifacts, sets)
}

// createArtifacts writes each artifact plus its platform set in one
// transaction. artifact_platforms carries the slot-uniqueness index, so a
// conflict surfaces from either insert; both are reported the same way.
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
// order it was published. The first entry is the canonical slot.
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

// ListArtifactsWithPlatforms lists a release's artifacts with each one's
// platform set attached, so a caller renders one entry per FILE rather than one
// per platform.
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

// ListArtifactsByPlatform lists a release's artifacts ONE ENTRY PER COVERED
// PLATFORM: an ordinary per-platform artifact yields itself, a file covering
// several yields one entry per platform with OS/Arch rewritten to that
// platform. Every surface that thinks in platforms -- apt, brew, npm, oci --
// consumes this, so a multi-platform artifact reaches exactly the platforms it
// would have reached as N separate rows. What changes is the row count and the
// number of download links, not the coverage.
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

// GetPlatformArtifact resolves one covered platform to the artifact serving it,
// with OS/Arch rewritten to the requested platform. Callers that derive a
// per-platform package (a deb's Architecture, an OCI config) need this rather
// than GetArtifact, whose OS/Arch are the artifact's canonical slot.
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
// pair; for one file covering several platforms every covered pair maps to the
// one slot, so all of them share a single download URL, digest and ETag.
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
