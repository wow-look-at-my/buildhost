package auth

import (
	"context"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

type contextKey int

const (
	tokenKey contextKey = iota
	projectKey
	routeKey
)

func WithToken(ctx context.Context, t *model.APIToken) context.Context {
	return context.WithValue(ctx, tokenKey, t)
}

func TokenFrom(ctx context.Context) *model.APIToken {
	t, _ := ctx.Value(tokenKey).(*model.APIToken)
	return t
}

func WithProject(ctx context.Context, p *model.Project) context.Context {
	return context.WithValue(ctx, projectKey, p)
}

func ProjectFrom(ctx context.Context) *model.Project {
	p, _ := ctx.Value(projectKey).(*model.Project)
	return p
}

func WithRouteInfo(ctx context.Context, ri RouteInfo) context.Context {
	return context.WithValue(ctx, routeKey, ri)
}

func RouteInfoFrom(ctx context.Context) RouteInfo {
	ri, _ := ctx.Value(routeKey).(RouteInfo)
	return ri
}
