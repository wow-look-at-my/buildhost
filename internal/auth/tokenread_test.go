package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestTokenCanReadProject(t *testing.T) {
	t.Serial()
	pub := &db.Project{ID: 1, Name: "pub"}
	priv := &db.Project{ID: 2, Name: "secret", IsPrivate: true}
	other := &db.Project{ID: 3, Name: "other-secret", IsPrivate: true}
	nsChild := &db.Project{ID: 4, Name: "repo/tool", IsPrivate: true}

	withTok := func(tok *db.APIToken) context.Context {
		return WithToken(context.Background(), tok)
	}
	privID := priv.ID

	t.Run("public always readable", func(t *testing.T) {
		assert.True(t, TokenCanReadProject(context.Background(), pub))
	})
	t.Run("private denied without token", func(t *testing.T) {
		assert.False(t, TokenCanReadProject(context.Background(), priv))
	})
	t.Run("private denied without read scope", func(t *testing.T) {
		ctx := withTok(&db.APIToken{ID: 1, Scopes: "write"})
		assert.False(t, TokenCanReadProject(ctx, priv))
	})
	t.Run("global read token sees any private project", func(t *testing.T) {
		ctx := withTok(&db.APIToken{ID: 1, Scopes: "read"})
		assert.True(t, TokenCanReadProject(ctx, priv))
		assert.True(t, TokenCanReadProject(ctx, other))
	})
	t.Run("project-scoped token sees only its project", func(t *testing.T) {
		ctx := withTok(&db.APIToken{ID: 1, Scopes: "read", ProjectID: &privID})
		assert.True(t, TokenCanReadProject(ctx, priv))
		assert.False(t, TokenCanReadProject(ctx, other))
	})
	t.Run("OIDC identity confined to its namespace", func(t *testing.T) {
		// An OIDC auto-provision token is global (no ProjectID) but carries a
		ctx := WithOIDCProject(withTok(&db.APIToken{ID: -1, Scopes: "read,write"}), "repo")
		assert.True(t, TokenCanReadProject(ctx, nsChild))
		assert.False(t, TokenCanReadProject(ctx, priv))
		assert.True(t, TokenCanReadProject(ctx, pub))
	})
}
