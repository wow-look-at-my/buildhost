package db

import (
	"context"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type DashboardStats struct {
	ProjectCount      int64
	ReleaseCount      int64
	ArtifactCount     int64
	TotalStorageBytes int64
	TokenCount        int64
	OIDCPolicyCount   int64
}

func (d *DB) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	row, err := d.q.GetDashboardStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("dashboard stats: %w", err)
	}
	return &DashboardStats{
		ProjectCount:      row.ProjectCount,
		ReleaseCount:      row.ReleaseCount,
		ArtifactCount:     row.ArtifactCount,
		TotalStorageBytes: row.TotalStorageBytes,
		TokenCount:        row.TokenCount,
		OIDCPolicyCount:   row.OidcPolicyCount,
	}, nil
}

type RecentRelease struct {
	model.Release
	ProjectName string
}

func (d *DB) ListRecentReleases(ctx context.Context, limit int) ([]RecentRelease, error) {
	rows, err := d.q.ListRecentReleases(ctx, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("list recent releases: %w", err)
	}
	releases := make([]RecentRelease, len(rows))
	for i, row := range rows {
		releases[i] = RecentRelease{
			Release: model.Release{
				ID:          row.ID,
				ProjectID:   row.ProjectID,
				Version:     row.Version,
				VersionNum:  row.VersionNum,
				GitBranch:   row.GitBranch,
				GitCommit:   row.GitCommit,
				Notes:       row.Notes,
				Published:   row.Published,
				CreatedAt:   row.CreatedAt,
				PublishedAt: row.PublishedAt,
			},
			ProjectName: row.ProjectName,
		}
	}
	return releases, nil
}

type ProjectSummary struct {
	model.Project
	ReleaseCount  int64
	ArtifactCount int64
}

func (d *DB) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := d.q.ListProjectSummaries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list project summaries: %w", err)
	}
	projects := make([]ProjectSummary, len(rows))
	for i, row := range rows {
		projects[i] = ProjectSummary{
			Project: model.Project{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
				Homepage:    row.Homepage,
				License:     row.License,
				IsPrivate:   row.IsPrivate,
				Versioning:  model.Versioning(row.Versioning),
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
			},
			ReleaseCount:  row.ReleaseCount,
			ArtifactCount: row.ArtifactCount,
		}
	}
	return projects, nil
}

type ReleaseSummary struct {
	model.Release
	ArtifactCount int64
}

func (d *DB) ListReleaseSummaries(ctx context.Context, projectID int64) ([]ReleaseSummary, error) {
	rows, err := d.q.ListReleaseSummaries(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list release summaries: %w", err)
	}
	releases := make([]ReleaseSummary, len(rows))
	for i, row := range rows {
		releases[i] = ReleaseSummary{
			Release: model.Release{
				ID:          row.ID,
				ProjectID:   row.ProjectID,
				Version:     row.Version,
				VersionNum:  row.VersionNum,
				GitBranch:   row.GitBranch,
				GitCommit:   row.GitCommit,
				Notes:       row.Notes,
				Published:   row.Published,
				CreatedAt:   row.CreatedAt,
				PublishedAt: row.PublishedAt,
			},
			ArtifactCount: row.ArtifactCount,
		}
	}
	return releases, nil
}

type TokenDetail struct {
	model.APIToken
	ProjectName string
}

func (d *DB) ListTokenDetails(ctx context.Context) ([]TokenDetail, error) {
	rows, err := d.q.ListTokenDetails(ctx)
	if err != nil {
		return nil, fmt.Errorf("list token details: %w", err)
	}
	tokens := make([]TokenDetail, len(rows))
	for i, row := range rows {
		tokens[i] = TokenDetail{
			APIToken: model.APIToken{
				ID:          row.ID,
				Name:        row.Name,
				TokenPrefix: row.TokenPrefix,
				ProjectID:   row.ProjectID,
				Scopes:      row.Scopes,
				ExpiresAt:   row.ExpiresAt,
				CreatedAt:   row.CreatedAt,
				LastUsedAt:  row.LastUsedAt,
			},
			ProjectName: row.ProjectName,
		}
	}
	return tokens, nil
}

type OIDCPolicyDetail struct {
	model.OIDCPolicy
	ProjectName string
}

func (d *DB) ListOIDCPolicyDetails(ctx context.Context) ([]OIDCPolicyDetail, error) {
	rows, err := d.q.ListOIDCPolicyDetails(ctx)
	if err != nil {
		return nil, fmt.Errorf("list oidc policy details: %w", err)
	}
	policies := make([]OIDCPolicyDetail, len(rows))
	for i, row := range rows {
		policies[i] = OIDCPolicyDetail{
			OIDCPolicy: model.OIDCPolicy{
				ID:             row.ID,
				Issuer:         row.Issuer,
				SubjectPattern: row.SubjectPattern,
				Audience:       row.Audience,
				ProjectID:      row.ProjectID,
				Scopes:         row.Scopes,
				CreatedAt:      row.CreatedAt,
			},
			ProjectName: row.ProjectName,
		}
	}
	return policies, nil
}
