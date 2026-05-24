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
