package db

import (
	"context"
	"fmt"
)

func (d *DB) GetDashboardStats(ctx context.Context) (*GetDashboardStatsRow, error) {
	row, err := d.q.GetDashboardStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard stats: %w", err)
	}
	return &row, nil
}

func (d *DB) ListRecentReleases(ctx context.Context, limit int) ([]ListRecentReleasesRow, error) {
	return d.q.ListRecentReleases(ctx, int64(limit))
}

func (d *DB) ListProjectSummaries(ctx context.Context) ([]ListProjectSummariesRow, error) {
	return d.q.ListProjectSummaries(ctx)
}

func (d *DB) ListReleaseSummaries(ctx context.Context, projectID int64) ([]ListReleaseSummariesRow, error) {
	return d.q.ListReleaseSummaries(ctx, projectID)
}

func (d *DB) ListTokenDetails(ctx context.Context) ([]ListTokenDetailsRow, error) {
	return d.q.ListTokenDetails(ctx)
}

func (d *DB) ListOIDCPolicyDetails(ctx context.Context) ([]ListOIDCPolicyDetailsRow, error) {
	return d.q.ListOIDCPolicyDetails(ctx)
}

func (d *DB) ListSiteDetails(ctx context.Context) ([]ListSiteDetailsRow, error) {
	return d.q.ListSiteDetails(ctx)
}

func (d *DB) ListAllArtifacts(ctx context.Context) ([]ListAllArtifactsRow, error) {
	return d.q.ListAllArtifacts(ctx)
}

// AllArtifactWithPlatforms is a dashboard artifact row plus every platform the
// file covers.
type AllArtifactWithPlatforms struct {
	ListAllArtifactsRow
	Platforms []Platform `json:"platforms"`
}

// ListAllArtifactsWithPlatforms lists every artifact once, carrying its full
// platform set, so the dashboard shows one row per FILE.
func (d *DB) ListAllArtifactsWithPlatforms(ctx context.Context) ([]AllArtifactWithPlatforms, error) {
	rows, err := d.q.ListAllArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	platformRows, err := d.q.ListAllArtifactPlatforms(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifact platforms: %w", err)
	}
	byArtifact := make(map[int64][]Platform, len(rows))
	for _, p := range platformRows {
		byArtifact[p.ArtifactID] = append(byArtifact[p.ArtifactID], Platform{OS: p.OS, Arch: p.Arch})
	}
	out := make([]AllArtifactWithPlatforms, len(rows))
	for i, r := range rows {
		out[i] = AllArtifactWithPlatforms{ListAllArtifactsRow: r, Platforms: byArtifact[r.ID]}
	}
	return out, nil
}

func (d *DB) GetStorageBreakdown(ctx context.Context) ([]GetStorageBreakdownRow, error) {
	return d.q.GetStorageBreakdown(ctx)
}
