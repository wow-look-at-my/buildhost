package auth

import (
	"context"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type contextKey int

const tokenKey contextKey = iota

func WithToken(ctx context.Context, t *model.APIToken) context.Context {
	return context.WithValue(ctx, tokenKey, t)
}

func TokenFrom(ctx context.Context) *model.APIToken {
	t, _ := ctx.Value(tokenKey).(*model.APIToken)
	return t
}

func IsAuthenticated(ctx context.Context) bool {
	return TokenFrom(ctx) != nil
}
