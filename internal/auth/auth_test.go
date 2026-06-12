package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestExtractToken_Bearer(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer my-secret-token")

	got := ExtractToken(r)
	require.Equal(t, "my-secret-token", got)

}

func TestExtractToken_BasicAuth(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	cred := base64.StdEncoding.EncodeToString([]byte("user:the-password-token"))
	r.Header.Set("Authorization", "Basic "+cred)

	got := ExtractToken(r)
	require.Equal(t, "the-password-token", got)

}

func TestExtractToken_QueryParam(t *testing.T) {
	r, _ := http.NewRequest("GET", "/?token=query-token-value", nil)

	got := ExtractToken(r)
	require.Equal(t, "query-token-value", got)

}

func TestExtractToken_NoAuth(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)

	got := ExtractToken(r)
	require.Equal(t, "", got)

}

func TestExtractToken_BearerTakesPrecedenceOverBasic(t *testing.T) {
	r, _ := http.NewRequest("GET", "/?token=query", nil)
	r.Header.Set("Authorization", "Bearer bearer-wins")

	got := ExtractToken(r)
	require.Equal(t, "bearer-wins", got)

}

func TestExtractToken_BasicTakesPrecedenceOverQuery(t *testing.T) {
	r, _ := http.NewRequest("GET", "/?token=query", nil)
	cred := base64.StdEncoding.EncodeToString([]byte("x:basic-wins"))
	r.Header.Set("Authorization", "Basic "+cred)

	got := ExtractToken(r)
	require.Equal(t, "basic-wins", got)

}

func TestExtractToken_InvalidBasicEncoding(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Basic %%%not-base64%%%")

	got := ExtractToken(r)
	require.Equal(t, "", got)

}

func TestWithToken_TokenFrom_RoundTrip(t *testing.T) {
	tok := &db.APIToken{
		ID:     42,
		Name:   "test-token",
		Scopes: "read,write",
	}

	ctx := context.Background()

	// Before setting, TokenFrom returns nil.
	require.Nil(t, TokenFrom(ctx))

	ctx = WithToken(ctx, tok)
	got := TokenFrom(ctx)
	require.NotNil(t, got)

	require.Equal(t, int64(42), got.ID)

	require.Equal(t, "test-token", got.Name)

	require.Equal(t, "read,write", got.Scopes)

}

func TestWithProject_ProjectFrom_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Before setting, ProjectFrom returns nil.
	require.Nil(t, ProjectFrom(ctx))

	proj := &db.Project{ID: 7, Name: "testproj"}
	ctx = WithProject(ctx, proj)
	got := ProjectFrom(ctx)
	require.NotNil(t, got)
	require.Equal(t, int64(7), got.ID)
	require.Equal(t, "testproj", got.Name)
}

type testRoute struct {
	project string
	access  AccessLevel
}

func (r testRoute) ProjectName() string { return r.project }
func (r testRoute) Access() AccessLevel { return r.access }

func TestWithRouteInfo_RouteInfoFrom_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Before setting, RouteInfoFrom returns nil (zero value of interface).
	ri := RouteInfoFrom(ctx)
	require.Nil(t, ri)

	ri2 := testRoute{project: "myapp", access: WriteAccess}
	ctx = WithRouteInfo(ctx, ri2)
	got := RouteInfoFrom(ctx)
	require.Equal(t, "myapp", got.ProjectName())
	require.Equal(t, WriteAccess, got.Access())
}
