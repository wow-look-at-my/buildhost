package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testAppKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// withStubGitHubApp points the resolver at a stub and configures a GitHub App,
// restoring all globals afterward.
func withStubGitHubApp(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	prevBase := gitHubAPIBase
	gitHubAPIBase = srv.URL
	require.NoError(t, SetGitHubApp("12345", testAppKeyPEM(t)))
	t.Cleanup(func() {
		gitHubAPIBase = prevBase
		_ = SetGitHubApp("", "")
		SetGitHubToken("")
		branchCacheMu.Lock()
		branchCache = map[string]branchCacheEntry{}
		branchCacheMu.Unlock()
		srv.Close()
	})
}

// goToolchainAppMux serves the App auth chain for wow-look-at-my/go-toolchain and
// records how many times each endpoint is hit.
func goToolchainAppMux(reposAuth *string, tokenMints, repoCalls *atomic.Int32) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/wow-look-at-my/go-toolchain/installation", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":42}`)
	})
	mux.HandleFunc("POST /app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		tokenMints.Add(1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_install","expires_at":"2999-01-01T00:00:00Z"}`)
	})
	mux.HandleFunc("GET /repos/wow-look-at-my/go-toolchain", func(w http.ResponseWriter, r *http.Request) {
		repoCalls.Add(1)
		if reposAuth != nil {
			*reposAuth = r.Header.Get("Authorization")
		}
		fmt.Fprint(w, `{"default_branch":"v1"}`)
	})
	return mux
}

func TestGitHubApp_UsesInstallationToken(t *testing.T) {
	var reposAuth string
	var mints, calls atomic.Int32
	withStubGitHubApp(t, goToolchainAppMux(&reposAuth, &mints, &calls))

	got := GitHubDefaultBranch(context.Background(), "wow-look-at-my/go-toolchain")
	assert.Equal(t, "v1", got)
	assert.Equal(t, "Bearer ghs_install", reposAuth, "repos call must use the installation token, not the app JWT")
	assert.Equal(t, int32(1), mints.Load())
}

func TestGitHubApp_CachesInstallationToken(t *testing.T) {
	var mints, calls atomic.Int32
	withStubGitHubApp(t, goToolchainAppMux(nil, &mints, &calls))

	ctx := context.Background()
	assert.Equal(t, "v1", GitHubDefaultBranch(ctx, "wow-look-at-my/go-toolchain"))
	// Force a fresh branch lookup (clear only the branch cache), so the repos call
	// repeats but the installation token is reused.
	branchCacheMu.Lock()
	branchCache = map[string]branchCacheEntry{}
	branchCacheMu.Unlock()
	assert.Equal(t, "v1", GitHubDefaultBranch(ctx, "wow-look-at-my/go-toolchain"))

	assert.Equal(t, int32(2), calls.Load(), "both branch lookups should reach the repos endpoint")
	assert.Equal(t, int32(1), mints.Load(), "installation token must be minted once and cached")
}

// When the app is not installed on the repo (installation discovery 404s), the
// lookup falls back to the static PAT.
func TestGitHubApp_FallsBackToPATWhenNotInstalled(t *testing.T) {
	var reposAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/acme/widget/installation", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /repos/acme/widget", func(w http.ResponseWriter, r *http.Request) {
		reposAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"default_branch":"main"}`)
	})
	withStubGitHubApp(t, mux)
	SetGitHubToken("ghp_fallback")

	got := GitHubDefaultBranch(context.Background(), "acme/widget")
	assert.Equal(t, "main", got)
	assert.Equal(t, "Bearer ghp_fallback", reposAuth, "uninstalled repo must fall back to the static PAT")
}
