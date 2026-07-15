package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDownloadToken_RoundTrip(t *testing.T) {
	tok := MintDownloadToken("vega-analyzer", "5", "linux", "amd64", "raw", false, time.Now().Add(time.Hour))
	assert.True(t, strings.HasPrefix(tok, "bhdl_"))
	assert.True(t, VerifyDownloadToken(tok, "vega-analyzer", "5", "linux", "amd64", "raw", false))
}

func TestDownloadToken_Expired(t *testing.T) {
	tok := MintDownloadToken("p", "1", "linux", "amd64", "raw", false, time.Now().Add(-time.Minute))
	assert.False(t, VerifyDownloadToken(tok, "p", "1", "linux", "amd64", "raw", false))
}

func TestDownloadToken_WrongTuple(t *testing.T) {
	tok := MintDownloadToken("p", "1", "linux", "amd64", "raw", false, time.Now().Add(time.Hour))
	cases := []struct {
		name                                     string
		project, version, osStr, archStr, fmtStr string
		debug                                    bool
	}{
		{"wrong project", "other", "1", "linux", "amd64", "raw", false},
		{"wrong version", "p", "2", "linux", "amd64", "raw", false},
		{"wrong os", "p", "1", "darwin", "amd64", "raw", false},
		{"wrong arch", "p", "1", "linux", "arm64", "raw", false},
		{"wrong fmt", "p", "1", "linux", "amd64", "tar.gz", false},
		{"wrong debug", "p", "1", "linux", "amd64", "raw", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.False(t, VerifyDownloadToken(tok, c.project, c.version, c.osStr, c.archStr, c.fmtStr, c.debug))
		})
	}
}

func TestDownloadToken_DebugBinding(t *testing.T) {
	tok := MintDownloadToken("p", "1", "linux", "amd64", "raw", true, time.Now().Add(time.Hour))
	assert.True(t, VerifyDownloadToken(tok, "p", "1", "linux", "amd64", "raw", true))
	assert.False(t, VerifyDownloadToken(tok, "p", "1", "linux", "amd64", "raw", false))
}

func TestDownloadToken_Malformed(t *testing.T) {
	assert.False(t, VerifyDownloadToken("", "p", "1", "linux", "amd64", "raw", false))
	assert.False(t, VerifyDownloadToken("nope", "p", "1", "linux", "amd64", "raw", false))
	assert.False(t, VerifyDownloadToken("bhdl_!!!", "p", "1", "linux", "amd64", "raw", false))
	assert.False(t, VerifyDownloadToken("bhdl_", "p", "1", "linux", "amd64", "raw", false))

	tok := MintDownloadToken("p", "1", "linux", "amd64", "raw", false, time.Now().Add(time.Hour))
	assert.False(t, VerifyDownloadToken(tok+"x", "p", "1", "linux", "amd64", "raw", false)) // tampered
}

func TestApexServiceURL(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{"pazer.build", "static.pazer.build"},                     // apex
		{"admin.pazer.build", "static.pazer.build"},               // admin subdomain
		{"dl.pazer.build", "static.pazer.build"},                  // service subdomain
		{"buildhost.example.com", "static.buildhost.example.com"}, // non-service apex left intact
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Host = c.host
		u := ApexServiceURL(req, "static")
		assert.Equal(t, c.want, u.Host, c.host)
		assert.Equal(t, "https", u.Scheme)
	}
}

func TestApexServiceURL_LocalhostScheme(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.Host = "admin.localhost:9090"
	u := ApexServiceURL(req, "static")
	assert.Equal(t, "http", u.Scheme)
	assert.Equal(t, "static.localhost:9090", u.Host)
}
