package static

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"direct peer, port stripped", "203.0.113.9:54321", "", "203.0.113.9"},
		{"single forwarded hop", "10.0.0.1:5000", "198.51.100.7", "198.51.100.7"},
		{"forwarded chain uses client hop", "10.0.0.1:5000", "198.51.100.7, 10.0.0.1, 10.0.0.2", "198.51.100.7"},
		{"forwarded value is trimmed", "10.0.0.1:5000", "  198.51.100.7  ", "198.51.100.7"},
		{"unparseable peer falls back verbatim", "unixsocket", "", "unixsocket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/file", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			assert.Equal(t, tt.want, clientIP(r))
		})
	}
}

func TestDownloadPrincipal(t *testing.T) {
	// Anonymous public pull: no user, no token.
	assert.Empty(t, downloadPrincipal(context.Background()))

	// An API token identifies as "token:<name>".
	tokCtx := auth.WithToken(context.Background(), &db.APIToken{Name: "ci-publisher"})
	assert.Equal(t, "token:ci-publisher", downloadPrincipal(tokCtx))

	// A signed-in user wins over any token also present.
	userCtx := auth.WithUser(tokCtx, "octocat")
	assert.Equal(t, "octocat", downloadPrincipal(userCtx))
}
