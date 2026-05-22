package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (d *DB) CreateOIDCPolicy(ctx context.Context, p *OIDCPolicy) error {
	res, err := d.q.InsertOIDCPolicy(ctx, InsertOIDCPolicyParams{
		Issuer:         p.Issuer,
		SubjectPattern: p.SubjectPattern,
		Audience:       p.Audience,
		ProjectID:      p.ProjectID,
		Scopes:         p.Scopes,
	})
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

func (d *DB) ListOIDCPolicies(ctx context.Context) ([]OIDCPolicy, error) {
	return d.q.ListAllOIDCPolicies(ctx)
}

func (d *DB) ListOIDCPoliciesByIssuer(ctx context.Context, issuer string) ([]OIDCPolicy, error) {
	return d.q.ListOIDCPoliciesByIssuer(ctx, issuer)
}

func (d *DB) DeleteOIDCPolicy(ctx context.Context, id int64) error {
	res, err := d.q.DeleteOIDCPolicyByID(ctx, id)
	if err != nil {
		return fmt.Errorf("delete oidc policy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) GetOIDCPolicyByID(ctx context.Context, id int64) (*OIDCPolicy, error) {
	row, err := d.q.GetOIDCPolicyByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get oidc policy: %w", err)
	}
	return &row, nil
}
