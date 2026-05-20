package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func (d *DB) CreateToken(ctx context.Context, name string, projectID *int64, scopes string) (plaintext string, token *model.APIToken, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	plaintext = "bh_" + base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(h[:])
	prefix := plaintext[:11]

	res, err := d.ExecContext(ctx,
		`INSERT INTO api_tokens (name, token_hash, token_prefix, project_id, scopes)
		 VALUES (?, ?, ?, ?, ?)`,
		name, hash, prefix, projectID, scopes)
	if err != nil {
		return "", nil, fmt.Errorf("insert token: %w", err)
	}

	id, _ := res.LastInsertId()
	token = &model.APIToken{
		ID:          id,
		Name:        name,
		TokenPrefix: prefix,
		ProjectID:   projectID,
		Scopes:      scopes,
	}
	return plaintext, token, nil
}

func (d *DB) LookupToken(ctx context.Context, plaintext string) (*model.APIToken, error) {
	h := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(h[:])

	t := &model.APIToken{}
	err := d.QueryRowContext(ctx,
		`SELECT id, name, token_hash, token_prefix, project_id, scopes, expires_at, created_at, last_used_at
		 FROM api_tokens WHERE token_hash = ?`, hash).Scan(
		&t.ID, &t.Name, &t.TokenHash, &t.TokenPrefix, &t.ProjectID, &t.Scopes, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup token: %w", err)
	}

	if t.IsExpired() {
		return nil, ErrNotFound
	}

	d.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = datetime('now') WHERE id = ?", t.ID)
	return t, nil
}

func (d *DB) ListTokens(ctx context.Context) ([]model.APIToken, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT id, name, token_prefix, project_id, scopes, expires_at, created_at, last_used_at
		 FROM api_tokens ORDER BY created_at DESC LIMIT 1000`)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()

	var tokens []model.APIToken
	for rows.Next() {
		var t model.APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.ProjectID, &t.Scopes, &t.ExpiresAt, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan token: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

func (d *DB) DeleteToken(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, "DELETE FROM api_tokens WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
