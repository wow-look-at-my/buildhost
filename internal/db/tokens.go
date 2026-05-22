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

	"github.com/wow-look-at-my/buildhost/internal/db/dbgen"
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

	res, err := d.q.InsertToken(ctx, dbgen.InsertTokenParams{
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		ProjectID:   projectID,
		Scopes:      scopes,
	})
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

	row, err := d.q.GetTokenByHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup token: %w", err)
	}

	t := &model.APIToken{
		ID:          row.ID,
		Name:        row.Name,
		TokenHash:   row.TokenHash,
		TokenPrefix: row.TokenPrefix,
		ProjectID:   row.ProjectID,
		Scopes:      row.Scopes,
		ExpiresAt:   row.ExpiresAt,
		CreatedAt:   row.CreatedAt,
		LastUsedAt:  row.LastUsedAt,
	}

	if t.IsExpired() {
		return nil, ErrNotFound
	}

	d.q.UpdateTokenLastUsed(ctx, t.ID)
	return t, nil
}

func (d *DB) ListTokens(ctx context.Context) ([]model.APIToken, error) {
	rows, err := d.q.ListAllTokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	tokens := make([]model.APIToken, len(rows))
	for i, row := range rows {
		tokens[i] = model.APIToken{
			ID:          row.ID,
			Name:        row.Name,
			TokenPrefix: row.TokenPrefix,
			ProjectID:   row.ProjectID,
			Scopes:      row.Scopes,
			ExpiresAt:   row.ExpiresAt,
			CreatedAt:   row.CreatedAt,
			LastUsedAt:  row.LastUsedAt,
		}
	}
	return tokens, nil
}

func (d *DB) DeleteToken(ctx context.Context, id int64) error {
	res, err := d.q.DeleteTokenByID(ctx, id)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
