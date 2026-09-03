package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubjectParsers_ImmutableIDs(t *testing.T) {
	t.Serial()
	tests := []struct {
		name     string
		subject  string
		org      string
		project  string
		repoPath string
	}{
		{
			name:     "classic",
			subject:  "repo:wow-look-at-my/tesla-wheel-data:ref:refs/heads/master",
			org:      "wow-look-at-my",
			project:  "tesla-wheel-data",
			repoPath: "wow-look-at-my/tesla-wheel-data",
		},
		{
			name:     "immutable both segments",
			subject:  "repo:wow-look-at-my@250878655/tesla-wheel-data@1307105896:ref:refs/heads/master",
			org:      "wow-look-at-my",
			project:  "tesla-wheel-data",
			repoPath: "wow-look-at-my/tesla-wheel-data",
		},
		{
			name:     "immutable owner only",
			subject:  "repo:myorg@123/myrepo:ref:refs/heads/main",
			org:      "myorg",
			project:  "myrepo",
			repoPath: "myorg/myrepo",
		},
		{
			name:     "immutable repo only",
			subject:  "repo:myorg/myrepo@456:pull_request",
			org:      "myorg",
			project:  "myrepo",
			repoPath: "myorg/myrepo",
		},
		{
			name:     "immutable uppercase normalized for project",
			subject:  "repo:MyOrg@1/MyRepo@2:pull_request",
			org:      "MyOrg",
			project:  "myrepo",
			repoPath: "MyOrg/MyRepo",
		},
		{
			// A non-numeric @-suffix is NOT an immutable ID: nothing is
			name:     "non-numeric suffix not stripped",
			subject:  "repo:myorg/bad@name:ref:refs/heads/main",
			org:      "myorg",
			project:  "",
			repoPath: "myorg/bad@name",
		},
		{
			// Only the LAST "@" is considered, so an ID after earlier junk is
			name:     "only last at-sign considered",
			subject:  "repo:myorg/we@ird@123:ref:refs/heads/main",
			org:      "myorg",
			project:  "",
			repoPath: "myorg/we@ird",
		},
		{
			name:     "trailing at-sign not stripped",
			subject:  "repo:myorg@/myrepo@:ref:refs/heads/main",
			org:      "myorg@",
			project:  "",
			repoPath: "myorg@/myrepo@",
		},
		{
			name:     "lone at-sign segment not stripped",
			subject:  "repo:myorg/@123:ref:refs/heads/main",
			org:      "myorg",
			project:  "",
			repoPath: "myorg/@123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.org, orgFromSubject(tt.subject), "orgFromSubject")
			assert.Equal(t, tt.project, projectFromSubject(tt.subject), "projectFromSubject")
			assert.Equal(t, tt.repoPath, repoPathFromSubject(tt.subject), "repoPathFromSubject")
		})
	}
}

// TestVerifyToken_TrustedIssuer_ImmutableSubject proves auto-provisioning
func TestVerifyToken_TrustedIssuer_ImmutableSubject(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-immutable")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:wow-look-at-my@250878655/tesla-wheel-data@1307105896:ref:refs/heads/master",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "push",
	}
	token := signJWT(t, key, "kid-immutable", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"wow-look-at-my"}, AllowedEvents: []string{"push"}})
	var vr VerifyResult
	tok, oidcProject, err := v.VerifyTokenFull(context.Background(), token, nil, &vr)
	require.NoError(t, err)
	assert.Equal(t, "tesla-wheel-data", oidcProject)
	assert.Equal(t, "wow-look-at-my/tesla-wheel-data", vr.RepoPath)
	// No dedicated ID claims on this token: the IDs come from the immutable
	assert.Equal(t, "250878655", vr.OwnerID)
	assert.Equal(t, "1307105896", vr.RepoID)
	assert.Equal(t, "oidc:repo:wow-look-at-my@250878655/tesla-wheel-data@1307105896:ref:refs/heads/master", tok.Name)
}

// TestVerifyToken_TrustedIssuer_ImmutableSubject_OrgCaseInsensitive combines
// the immutable-ID stripping with the case-insensitive org allowlist match.
func TestVerifyToken_TrustedIssuer_ImmutableSubject_OrgCaseInsensitive(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-immutable-case")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:PazerOP@99/scratch@100:pull_request",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "pull_request",
	}
	token := signJWT(t, key, "kid-immutable-case", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"pazerop"}, AllowedEvents: []string{"pull_request"}})
	_, oidcProject, err := v.VerifyToken(context.Background(), token, nil)
	require.NoError(t, err)
	assert.Equal(t, "scratch", oidcProject)
}

// The dedicated repository / repository_owner / *_id claims are preferred over
// subject parsing, and the numeric IDs are surfaced for pinning -- from the
// claims when present, else from an immutable subject's @id suffixes.
func TestVerifyToken_TrustedIssuer_DedicatedClaimsPreferred(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-dedicated")

	claims := map[string]any{
		"iss":                 srv.URL,
		"sub":                 "repo:wow-look-at-my@250878655/tesla-wheel-data@1307105896:ref:refs/heads/master",
		"exp":                 time.Now().Add(10 * time.Minute).Unix(),
		"iat":                 time.Now().Unix(),
		"event_name":          "push",
		"repository":          "wow-look-at-my/tesla-wheel-data",
		"repository_id":       "1307105896",
		"repository_owner":    "wow-look-at-my",
		"repository_owner_id": "250878655",
	}
	token := signJWT(t, key, "kid-dedicated", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"wow-look-at-my"}, AllowedEvents: []string{"push"}})
	var vr VerifyResult
	_, oidcProject, err := v.VerifyTokenFull(context.Background(), token, nil, &vr)
	require.NoError(t, err)
	assert.Equal(t, "tesla-wheel-data", oidcProject)
	assert.Equal(t, "wow-look-at-my/tesla-wheel-data", vr.RepoPath)
	assert.Equal(t, "250878655", vr.OwnerID)
	assert.Equal(t, "1307105896", vr.RepoID)
}

// A classic-era token (classic sub) that still mints the dedicated ID claims
// -- which GitHub does for every repo -- surfaces the IDs for pinning too.
func TestVerifyToken_TrustedIssuer_ClassicSubWithIDClaims(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-classic-ids")

	claims := map[string]any{
		"iss":                 srv.URL,
		"sub":                 "repo:myorg/myrepo:ref:refs/heads/main",
		"exp":                 time.Now().Add(10 * time.Minute).Unix(),
		"iat":                 time.Now().Unix(),
		"event_name":          "push",
		"repository":          "myorg/myrepo",
		"repository_id":       "42",
		"repository_owner":    "myorg",
		"repository_owner_id": "7",
	}
	token := signJWT(t, key, "kid-classic-ids", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"myorg"}, AllowedEvents: []string{"push"}})
	var vr VerifyResult
	_, oidcProject, err := v.VerifyTokenFull(context.Background(), token, nil, &vr)
	require.NoError(t, err)
	assert.Equal(t, "myrepo", oidcProject)
	assert.Equal(t, "7", vr.OwnerID)
	assert.Equal(t, "42", vr.RepoID)
}

func TestOrgAllowed(t *testing.T) {
	t.Serial()
	tests := []struct {
		name    string
		allowed []string
		org     string
		ownerID string
		want    bool
	}{
		{"wildcard", []string{"*"}, "anyone", "1", true},
		{"plain name matches any id", []string{"myorg"}, "myorg", "123", true},
		{"plain name matches no id", []string{"myorg"}, "myorg", "", true},
		{"plain name case-insensitive", []string{"pazerop"}, "PazerOP", "9", true},
		{"pinned entry matches name and id", []string{"MyOrg@123"}, "myorg", "123", true},
		{"pinned entry rejects wrong id", []string{"myorg@123"}, "myorg", "999", false},
		{"pinned entry rejects id-less token", []string{"myorg@123"}, "myorg", "", false},
		{"pinned plus plain entry falls through", []string{"myorg@123", "myorg"}, "myorg", "", true},
		{"unrelated org rejected", []string{"otherorg"}, "myorg", "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, orgAllowed(tt.allowed, tt.org, tt.ownerID))
		})
	}
}

// An allowlist entry pinned to a different owner ID refuses the token even
// though the org NAME matches -- the resurrection case for the allowlist
// itself -- and the error names the offending ID.
func TestVerifyToken_TrustedIssuer_OrgIDPinned_Mismatch(t *testing.T) {
	t.Serial()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	srv := jwksServer(t, &key.PublicKey, "kid-org-id-pin")

	claims := map[string]any{
		"iss":        srv.URL,
		"sub":        "repo:wow-look-at-my@250878655/tesla-wheel-data@1307105896:ref:refs/heads/master",
		"exp":        time.Now().Add(10 * time.Minute).Unix(),
		"iat":        time.Now().Unix(),
		"event_name": "push",
	}
	token := signJWT(t, key, "kid-org-id-pin", claims)

	v := NewOIDCVerifier(OIDCConfig{TrustedIssuers: []string{srv.URL}, AllowedOrgs: []string{"wow-look-at-my@999"}, AllowedEvents: []string{"push"}})
	_, _, err = v.VerifyToken(context.Background(), token, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in allowed list")
	assert.Contains(t, err.Error(), "owner id 250878655")
}
