package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Tokens ------------------------------------------------------------------

func TestCreateAndLookupToken(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "ci-token", nil, "read,write")
	require.Nil(t, err)

	assert.True(t, strings.HasPrefix(plaintext, "bh_"))

	require.NotEqual(t, int64(0), tok.ID)

	assert.Equal(t, "ci-token", tok.Name)

	assert.Equal(t, "read,write", tok.Scopes)

	looked, err := d.LookupToken(ctx, plaintext)
	require.Nil(t, err)

	assert.Equal(t, tok.ID, looked.ID)

	assert.Equal(t, "ci-token", looked.Name)

}

func TestLookupTokenNotFound(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	_, err := d.LookupToken(context.Background(), "bh_bogus_token_value_here")
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestCreateTokenWithProjectScope(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	p := &Project{Name: "scoped", Versioning: VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, p))

	pid := p.ID
	_, tok, err := d.CreateToken(ctx, "proj-token", &pid, "read")
	require.Nil(t, err)

	assert.False(t, tok.ProjectID == nil || *tok.ProjectID != p.ID)

}

func TestListTokens(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"token-a", "token-b", "token-c"} {
		_, _, err := d.CreateToken(ctx, name, nil, "read")
		require.Nil(t, err)

	}

	list, err := d.ListTokens(ctx)
	require.Nil(t, err)

	require.Equal(t, 3, len(list))

}

func TestDeleteToken(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "doomed", nil, "read")
	require.Nil(t, err)

	require.NoError(t, d.DeleteToken(ctx, tok.ID))

	_, err = d.LookupToken(ctx, plaintext)
	assert.True(t, errors.Is(err, ErrNotFound))

}

func TestDeleteTokenNotFound(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	err := d.DeleteToken(context.Background(), 99999)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestLookupToken_ExpiredTokenReturnsNotFound(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "expiring", nil, "read")
	require.NoError(t, err)

	_, err = d.ExecContext(ctx,
		"UPDATE api_tokens SET expires_at = datetime('now', '-1 hour') WHERE id = ?", tok.ID)
	require.NoError(t, err)

	_, err = d.LookupToken(ctx, plaintext)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestLookupToken_FutureExpirySucceeds(t *testing.T) {
	t.Serial()
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "valid-future", nil, "read")
	require.NoError(t, err)

	_, err = d.ExecContext(ctx,
		"UPDATE api_tokens SET expires_at = datetime('now', '+1 hour') WHERE id = ?", tok.ID)
	require.NoError(t, err)

	got, err := d.LookupToken(ctx, plaintext)
	require.NoError(t, err)
	assert.Equal(t, tok.ID, got.ID)
}
