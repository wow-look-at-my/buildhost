package apt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

// privateGet sends a request for apt.example.com/private/<sub> through the real
// token middleware, with the token as the Basic password apt sends.
func privateGet(t *testing.T, h *Handler, d *db.DB, sub, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/private/"+sub, nil)
	req.Host = "apt.example.com"
	if token != "" {
		req.SetBasicAuth("token", token)
	}
	rec := httptest.NewRecorder()
	(&auth.Middleware{DB: d}).Authenticate(http.HandlerFunc(h.ServePrivate)).ServeHTTP(rec, req)
	return rec
}

// privateFixture holds a public project, a private one the token reads and a private one it does not.
func privateFixture(t *testing.T) (*Handler, *db.DB, string) {
	t.Helper()
	h, d, store := setupTest(t)
	ctx := context.Background()
	seedAptProject(t, d, store, "pubapp", "public-bytes", false)
	mine, _, _ := seedAptProject(t, d, store, "secret-dep", "secret-bytes", false)
	other, _, _ := seedAptProject(t, d, store, "other-secret", "other-bytes", false)
	require.NoError(t, d.SetProjectVisibility(ctx, mine.ID, true))
	require.NoError(t, d.SetProjectVisibility(ctx, other.ID, true))
	token, _, err := d.CreateToken(ctx, "reader", &mine.ID, "read")
	require.NoError(t, err)
	h.Signer = NewSigner(t.TempDir())
	return h, d, token
}

func TestServePrivate_ChallengesWithoutACredential(t *testing.T) {
	h, d, _ := privateFixture(t)
	for _, token := range []string{"", "bh_not_a_real_token"} {
		rec := privateGet(t, h, d, "dists/stable/InRelease", token)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "token %q", token)
		assert.Equal(t, `Basic realm="buildhost"`, rec.Header().Get("Www-Authenticate"), "apt sends auth.conf only after a challenge")
		assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestServePrivate_ScopeIsWhatTheTokenReads(t *testing.T) {
	h, d, token := privateFixture(t)

	rec := privateGet(t, h, d, "dists/stable/main/binary-amd64/Packages", token)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "Package: pubapp\n", "every public project is in the scope")
	assert.Contains(t, body, "Package: secret-dep\n", "a private project the token reads is in the scope")
	assert.NotContains(t, body, "other-secret", "a private project the token cannot read is not named at all")
	assert.Contains(t, body, "Filename: pool/secret-dep/secret-dep_1.2.3_amd64.deb\n")
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "Authorization", rec.Header().Get("Vary"))
}

func TestServePrivate_ReleaseHashesTheServedIndex(t *testing.T) {
	h, d, token := privateFixture(t)

	packages := privateGet(t, h, d, "dists/stable/main/binary-amd64/Packages", token).Body.String()
	idx, err := h.scopeIndex(withToken(t, d, token))
	require.NoError(t, err)
	hashes, err := packagesHashes(idx)
	require.NoError(t, err)
	require.Len(t, hashes, 1)
	assert.Equal(t, len(packages), hashes[0].Size)

	rec := privateGet(t, h, d, "dists/stable/InRelease", token)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "-----BEGIN PGP SIGNED MESSAGE-----")
	assert.Contains(t, rec.Body.String(), "Label: private\n")
	assert.Contains(t, rec.Body.String(), " "+hashes[0].Hash+" ")
	assert.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	key := privateGet(t, h, d, "key.asc", token)
	require.Equal(t, http.StatusOK, key.Code)
	assert.Equal(t, "private, no-store", key.Header().Get("Cache-Control"))
}

func TestServePrivate_PoolSignsAPrivateRedirect(t *testing.T) {
	h, d, token := privateFixture(t)

	rec := privateGet(t, h, d, "pool/secret-dep/secret-dep_1.2.3_amd64.deb", token)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	loc := rec.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "https://static.example.com/file?"), loc)
	assert.Contains(t, loc, "project=secret-dep")
	assert.Contains(t, loc, "token=", "a credential for the apt host alone must reach the deb")

	rec = privateGet(t, h, d, "pool/pubapp/pubapp_1.2.3_amd64.deb", token)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.NotContains(t, rec.Header().Get("Location"), "token=")

	rec = privateGet(t, h, d, "pool/other-secret/other-secret_1.2.3_amd64.deb", token)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a project outside the scope is absent")
}

// withToken is a request that carries token the way the middleware resolves it.
func withToken(t *testing.T, d *db.DB, token string) *http.Request {
	t.Helper()
	tok, err := d.LookupToken(context.Background(), token)
	require.NoError(t, err)
	req := httptest.NewRequest("GET", "/private/dists/stable/Release", nil)
	req.Host = "apt.example.com"
	return req.WithContext(auth.WithToken(req.Context(), tok))
}
