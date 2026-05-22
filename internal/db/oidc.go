package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/db/dbgen"
	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateOIDCPolicy(ctx context.Context, p *model.OIDCPolicy) error {
	res, err := d.q.InsertOIDCPolicy(ctx, dbgen.InsertOIDCPolicyParams{
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

func (d *DB) ListOIDCPolicies(ctx context.Context) ([]model.OIDCPolicy, error) {
	rows, err := d.q.ListAllOIDCPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list oidc policies: %w", err)
	}
	return oidcPoliciesFromRows(rows), nil
}

func (d *DB) ListOIDCPoliciesByIssuer(ctx context.Context, issuer string) ([]model.OIDCPolicy, error) {
	rows, err := d.q.ListOIDCPoliciesByIssuer(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("list oidc policies by issuer: %w", err)
	}
	return oidcPoliciesFromRows(rows), nil
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

func (d *DB) GetOIDCPolicyByID(ctx context.Context, id int64) (*model.OIDCPolicy, error) {
	row, err := d.q.GetOIDCPolicyByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get oidc policy: %w", err)
	}
	return oidcPolicyFromRow(row), nil
}

func oidcPolicyFromRow(row dbgen.OidcPolicy) *model.OIDCPolicy {
	return &model.OIDCPolicy{
		ID:             row.ID,
		Issuer:         row.Issuer,
		SubjectPattern: row.SubjectPattern,
		Audience:       row.Audience,
		ProjectID:      row.ProjectID,
		Scopes:         row.Scopes,
		CreatedAt:      row.CreatedAt,
	}
}

func oidcPoliciesFromRows(rows []dbgen.OidcPolicy) []model.OIDCPolicy {
	policies := make([]model.OIDCPolicy, len(rows))
	for i, row := range rows {
		policies[i] = *oidcPolicyFromRow(row)
	}
	return policies
}
