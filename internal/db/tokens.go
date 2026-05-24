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
)

func (d *DB) CreateToken(ctx context.Context, name string, projectID *int64, scopes string) (plaintext string, token *APIToken, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	plaintext = "bh_" + base64.RawURLEncoding.EncodeToString(raw)
	h := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(h[:])
	prefix := plaintext[:11]

	res, err := d.q.InsertToken(ctx, InsertTokenParams{
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
	token = &APIToken{
		ID:          id,
		Name:        name,
		TokenPrefix: prefix,
		ProjectID:   projectID,
		Scopes:      scopes,
	}
	return plaintext, token, nil
}

func (d *DB) LookupToken(ctx context.Context, plaintext string) (*APIToken, error) {
	h := sha256.Sum256([]byte(plaintext))
	hash := hex.EncodeToString(h[:])

	row, err := d.q.GetTokenByHash(ctx, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup token: %w", err)
	}

	t := &row
	if t.IsExpired() {
		return nil, ErrNotFound
	}

	d.q.UpdateTokenLastUsed(ctx, t.ID)
	return t, nil
}

func (d *DB) ListTokens(ctx context.Context) ([]ListAllTokensRow, error) {
	return d.q.ListAllTokens(ctx)
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
