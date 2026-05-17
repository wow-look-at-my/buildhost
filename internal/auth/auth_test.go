package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func TestExtractToken_Bearer(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer my-secret-token")

	got := ExtractToken(r)
	if got != "my-secret-token" {
		t.Fatalf("ExtractToken(Bearer) = %q, want %q", got, "my-secret-token")
	}
}

func TestExtractToken_BasicAuth(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	cred := base64.StdEncoding.EncodeToString([]byte("user:the-password-token"))
	r.Header.Set("Authorization", "Basic "+cred)

	got := ExtractToken(r)
	if got != "the-password-token" {
		t.Fatalf("ExtractToken(Basic) = %q, want %q", got, "the-password-token")
	}
}

func TestExtractToken_QueryParam(t *testing.T) {
	r, _ := http.NewRequest("GET", "/?token=query-token-value", nil)

	got := ExtractToken(r)
	if got != "query-token-value" {
		t.Fatalf("ExtractToken(query) = %q, want %q", got, "query-token-value")
	}
}

func TestExtractToken_NoAuth(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)

	got := ExtractToken(r)
	if got != "" {
		t.Fatalf("ExtractToken(no auth) = %q, want empty string", got)
	}
}

func TestExtractToken_BearerTakesPrecedenceOverBasic(t *testing.T) {
	r, _ := http.NewRequest("GET", "/?token=query", nil)
	r.Header.Set("Authorization", "Bearer bearer-wins")

	got := ExtractToken(r)
	if got != "bearer-wins" {
		t.Fatalf("ExtractToken(precedence) = %q, want %q", got, "bearer-wins")
	}
}

func TestExtractToken_BasicTakesPrecedenceOverQuery(t *testing.T) {
	r, _ := http.NewRequest("GET", "/?token=query", nil)
	cred := base64.StdEncoding.EncodeToString([]byte("x:basic-wins"))
	r.Header.Set("Authorization", "Basic "+cred)

	got := ExtractToken(r)
	if got != "basic-wins" {
		t.Fatalf("ExtractToken(precedence) = %q, want %q", got, "basic-wins")
	}
}

func TestExtractToken_InvalidBasicEncoding(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Basic %%%not-base64%%%")

	got := ExtractToken(r)
	if got != "" {
		t.Fatalf("ExtractToken(bad base64) = %q, want empty", got)
	}
}

func TestWithToken_TokenFrom_RoundTrip(t *testing.T) {
	tok := &model.APIToken{
		ID:     42,
		Name:   "test-token",
		Scopes: "read,write",
	}

	ctx := context.Background()

	// Before setting, TokenFrom returns nil.
	if got := TokenFrom(ctx); got != nil {
		t.Fatalf("TokenFrom(empty ctx) = %v, want nil", got)
	}

	ctx = WithToken(ctx, tok)
	got := TokenFrom(ctx)
	if got == nil {
		t.Fatal("TokenFrom(ctx with token) = nil, want non-nil")
	}
	if got.ID != 42 {
		t.Fatalf("TokenFrom.ID = %d, want 42", got.ID)
	}
	if got.Name != "test-token" {
		t.Fatalf("TokenFrom.Name = %q, want %q", got.Name, "test-token")
	}
	if got.Scopes != "read,write" {
		t.Fatalf("TokenFrom.Scopes = %q, want %q", got.Scopes, "read,write")
	}
}

func TestIsAuthenticated(t *testing.T) {
	ctx := context.Background()
	if IsAuthenticated(ctx) {
		t.Fatal("IsAuthenticated(empty ctx) = true, want false")
	}

	ctx = WithToken(ctx, &model.APIToken{ID: 1, Scopes: "read"})
	if !IsAuthenticated(ctx) {
		t.Fatal("IsAuthenticated(ctx with token) = false, want true")
	}
}
