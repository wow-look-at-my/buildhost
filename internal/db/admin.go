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
	LogicalBytes      int64
	PhysicalBytes     int64
}

func (d *DB) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	s := &DashboardStats{}
	err := d.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM projects),
			(SELECT COUNT(*) FROM releases),
			(SELECT COUNT(*) FROM artifacts),
			(SELECT COALESCE(SUM(size), 0) FROM artifacts),
			(SELECT COUNT(*) FROM api_tokens),
			(SELECT COUNT(*) FROM oidc_policies),
			(SELECT COALESCE(SUM(size), 0) FROM artifacts)
				+ (SELECT COALESCE(SUM(CASE WHEN stripped_storage_key != '' THEN stripped_size ELSE 0 END), 0) FROM artifacts)
				+ (SELECT COALESCE(SUM(CASE WHEN debug_storage_key != '' THEN debug_size ELSE 0 END), 0) FROM artifacts)
				+ (SELECT COALESCE(SUM(size), 0) FROM packaged_artifacts),
			(SELECT COALESCE(SUM(sz), 0) FROM (
				SELECT k, MAX(sz) AS sz FROM (
					SELECT storage_key AS k, size AS sz FROM artifacts
					UNION ALL
					SELECT stripped_storage_key, stripped_size FROM artifacts WHERE stripped_storage_key != ''
					UNION ALL
					SELECT debug_storage_key, debug_size FROM artifacts WHERE debug_storage_key != ''
					UNION ALL
					SELECT storage_key, size FROM packaged_artifacts
				) GROUP BY k
			))
	`).Scan(&s.ProjectCount, &s.ReleaseCount, &s.ArtifactCount, &s.TotalStorageBytes, &s.TokenCount, &s.OIDCPolicyCount, &s.LogicalBytes, &s.PhysicalBytes)
	if err != nil {
		return nil, fmt.Errorf("dashboard stats: %w", err)
	}
	return s, nil
}

type RecentRelease struct {
	model.Release
	ProjectName string
}

func (d *DB) ListRecentReleases(ctx context.Context, limit int) ([]RecentRelease, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit,
		       r.notes, r.published, r.created_at, r.published_at, p.name
		FROM releases r
		JOIN projects p ON r.project_id = p.id
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent releases: %w", err)
	}
	defer rows.Close()

	var releases []RecentRelease
	for rows.Next() {
		var r RecentRelease
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Version, &r.VersionNum, &r.GitBranch, &r.GitCommit,
			&r.Notes, &r.Published, &r.CreatedAt, &r.PublishedAt, &r.ProjectName); err != nil {
			return nil, fmt.Errorf("scan recent release: %w", err)
		}
		releases = append(releases, r)
	}
	return releases, rows.Err()
}

type ProjectSummary struct {
	model.Project
	ReleaseCount  int64
	ArtifactCount int64
}

func (d *DB) ListProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT p.id, p.name, p.description, p.homepage, p.license, p.is_private, p.versioning,
		       p.created_at, p.updated_at,
		       (SELECT COUNT(*) FROM releases WHERE project_id = p.id) AS release_count,
		       (SELECT COUNT(*) FROM artifacts a JOIN releases r ON a.release_id = r.id WHERE r.project_id = p.id) AS artifact_count
		FROM projects p
		ORDER BY p.name`)
	if err != nil {
		return nil, fmt.Errorf("list project summaries: %w", err)
	}
	defer rows.Close()

	var projects []ProjectSummary
	for rows.Next() {
		var p ProjectSummary
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Homepage, &p.License, &p.IsPrivate, &p.Versioning,
			&p.CreatedAt, &p.UpdatedAt, &p.ReleaseCount, &p.ArtifactCount); err != nil {
			return nil, fmt.Errorf("scan project summary: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

type ReleaseSummary struct {
	model.Release
	ArtifactCount int64
}

func (d *DB) ListReleaseSummaries(ctx context.Context, projectID int64) ([]ReleaseSummary, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT r.id, r.project_id, r.version, r.version_num, r.git_branch, r.git_commit,
		       r.notes, r.published, r.created_at, r.published_at,
		       (SELECT COUNT(*) FROM artifacts WHERE release_id = r.id) AS artifact_count
		FROM releases r
		WHERE r.project_id = ?
		ORDER BY r.version_num DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list release summaries: %w", err)
	}
	defer rows.Close()

	var releases []ReleaseSummary
	for rows.Next() {
		var r ReleaseSummary
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Version, &r.VersionNum, &r.GitBranch, &r.GitCommit,
			&r.Notes, &r.Published, &r.CreatedAt, &r.PublishedAt, &r.ArtifactCount); err != nil {
			return nil, fmt.Errorf("scan release summary: %w", err)
		}
		releases = append(releases, r)
	}
	return releases, rows.Err()
}

type TokenDetail struct {
	model.APIToken
	ProjectName string
}

func (d *DB) ListTokenDetails(ctx context.Context) ([]TokenDetail, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT t.id, t.name, t.token_prefix, t.project_id, t.scopes, t.expires_at, t.created_at, t.last_used_at,
		       COALESCE(p.name, '') AS project_name
		FROM api_tokens t
		LEFT JOIN projects p ON t.project_id = p.id
		ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list token details: %w", err)
	}
	defer rows.Close()

	var tokens []TokenDetail
	for rows.Next() {
		var t TokenDetail
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.ProjectID, &t.Scopes, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt,
			&t.ProjectName); err != nil {
			return nil, fmt.Errorf("scan token detail: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

type OIDCPolicyDetail struct {
	model.OIDCPolicy
	ProjectName string
}

func (d *DB) ListOIDCPolicyDetails(ctx context.Context) ([]OIDCPolicyDetail, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT o.id, o.issuer, o.subject_pattern, o.audience, o.project_id, o.scopes, o.created_at,
		       COALESCE(p.name, '') AS project_name
		FROM oidc_policies o
		LEFT JOIN projects p ON o.project_id = p.id
		ORDER BY o.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list oidc policy details: %w", err)
	}
	defer rows.Close()

	var policies []OIDCPolicyDetail
	for rows.Next() {
		var p OIDCPolicyDetail
		if err := rows.Scan(&p.ID, &p.Issuer, &p.SubjectPattern, &p.Audience, &p.ProjectID, &p.Scopes, &p.CreatedAt,
			&p.ProjectName); err != nil {
			return nil, fmt.Errorf("scan oidc policy detail: %w", err)
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}
