package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateOIDCPolicy(ctx context.Context, p *model.OIDCPolicy) error {
	res, err := d.ExecContext(ctx,
		`INSERT INTO oidc_policies (issuer, subject_pattern, project_id, scopes)
		 VALUES (?, ?, ?, ?)`,
		p.Issuer, p.SubjectPattern, p.ProjectID, p.Scopes)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("create oidc policy: %w", err)
	}
	id, _ := res.LastInsertId()
	p.ID = id
	return nil
}

func (d *DB) ListOIDCPolicies(ctx context.Context) ([]model.OIDCPolicy, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, issuer, subject_pattern, project_id, scopes, created_at
		 FROM oidc_policies ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list oidc policies: %w", err)
	}
	defer rows.Close()

	var policies []model.OIDCPolicy
	for rows.Next() {
		var p model.OIDCPolicy
		if err := rows.Scan(&p.ID, &p.Issuer, &p.SubjectPattern, &p.ProjectID, &p.Scopes, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan oidc policy: %w", err)
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (d *DB) ListOIDCPoliciesByIssuer(ctx context.Context, issuer string) ([]model.OIDCPolicy, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, issuer, subject_pattern, project_id, scopes, created_at
		 FROM oidc_policies WHERE issuer = ?`, issuer)
	if err != nil {
		return nil, fmt.Errorf("list oidc policies by issuer: %w", err)
	}
	defer rows.Close()

	var policies []model.OIDCPolicy
	for rows.Next() {
		var p model.OIDCPolicy
		if err := rows.Scan(&p.ID, &p.Issuer, &p.SubjectPattern, &p.ProjectID, &p.Scopes, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan oidc policy: %w", err)
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (d *DB) DeleteOIDCPolicy(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, "DELETE FROM oidc_policies WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete oidc policy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) GetOIDCPolicyByID(ctx context.Context, id int64) (*model.OIDCPolicy, error) {
	p := &model.OIDCPolicy{}
	err := d.QueryRowContext(ctx,
		`SELECT id, issuer, subject_pattern, project_id, scopes, created_at
		 FROM oidc_policies WHERE id = ?`, id).Scan(
		&p.ID, &p.Issuer, &p.SubjectPattern, &p.ProjectID, &p.Scopes, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get oidc policy: %w", err)
	}
	return p, nil
}
