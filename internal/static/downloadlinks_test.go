package static

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func TestRoute_AllowsPublicRead(t *testing.T) {
	tok := auth.MintDownloadToken("vega", "5", "linux", "amd64", "raw", false, time.Now().Add(time.Hour))

	mk := func(query string) route {
		req := httptest.NewRequest("GET", "/file?"+query, nil)
		return parseRoute(req).(route)
	}
	proj := &db.Project{Name: "vega"}

	// A valid signed token for the exact artifact authorizes the read.
	good := mk("project=vega&v=5&os=linux&arch=amd64&fmt=raw&token=" + tok)
	assert.True(t, good.AllowsPublicRead(context.Background(), nil, proj))

	// fmt defaults to raw when omitted, so a raw-bound token still matches.
	defFmt := mk("project=vega&v=5&os=linux&arch=amd64&token=" + tok)
	assert.True(t, defFmt.AllowsPublicRead(context.Background(), nil, proj))

	// No token: the private gate stays closed.
	assert.False(t, mk("project=vega&v=5&os=linux&arch=amd64&fmt=raw").
		AllowsPublicRead(context.Background(), nil, proj))

	// Token bound to a different artifact (arch) is rejected.
	assert.False(t, mk("project=vega&v=5&os=linux&arch=arm64&fmt=raw&token="+tok).
		AllowsPublicRead(context.Background(), nil, proj))

	// Token from a different project does not validate here.
	assert.False(t, mk("project=vega&v=5&os=linux&arch=amd64&fmt=raw&token="+tok).
		AllowsPublicRead(context.Background(), nil, &db.Project{Name: "other"}))
}

func TestSignedURL(t *testing.T) {
	staticBase, _ := url.Parse("https://static.example.com")
	p := For("vega").WithVersion("5").WithOS("linux").WithArch("amd64").WithFmt("raw")
	urlStr, tok := SignedURL(staticBase, p, time.Now().Add(time.Hour))

	assert.Contains(t, urlStr, "https://static.example.com/file?")
	assert.Contains(t, urlStr, "token="+tok)
	// Query is canonical (sorted) so the static handler serves it without a redirect.
	assert.Contains(t, urlStr, "arch=amd64&fmt=raw&os=linux&project=vega&token=")
	assert.True(t, auth.VerifyDownloadToken(tok, "vega", "5", "linux", "amd64", "raw", false))
}
