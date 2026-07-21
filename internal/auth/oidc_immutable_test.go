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

// GitHub repos created after 2026-07-15 mint "immutable" OIDC subjects that
// suffix each repo-path segment with its numeric account/repo ID --
// `repo:OWNER@OWNERID/REPO@REPOID:ref:refs/heads/BRANCH` -- while classic
// repos keep the bare `repo:OWNER/REPO:...` form (see
// https://github.blog/changelog/2026-04-23-immutable-subject-claims-for-github-actions-oidc-tokens/).
// All three subject parsers must strip the IDs and behave identically to the
// classic form; classic subjects must pass through byte-for-byte.
func TestSubjectParsers_ImmutableIDs(t *testing.T) {
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
			// stripped, and the "@" then fails project-name validation as
			// before.
			name:     "non-numeric suffix not stripped",
			subject:  "repo:myorg/bad@name:ref:refs/heads/main",
			org:      "myorg",
			project:  "",
			repoPath: "myorg/bad@name",
		},
		{
			// Only the LAST "@" is considered, so an ID after earlier junk is
			// still stripped -- but the leftover "@" fails validation.
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
			// "@123" would strip to an empty name; leave it alone (and let
			// validation reject it) instead.
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
// works end-to-end for a post-2026-07-15 repo: the org allowlist matches the
// owner NAME (not "name@id"), the derived project name passes validation, and
// VerifyResult.RepoPath is the clean "owner/repo" the GitHub REST lookups
// need. The token name keeps the raw subject, IDs included.
func TestVerifyToken_TrustedIssuer_ImmutableSubject(t *testing.T) {
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
	assert.Equal(t, "oidc:repo:wow-look-at-my@250878655/tesla-wheel-data@1307105896:ref:refs/heads/master", tok.Name)
}

// TestVerifyToken_TrustedIssuer_ImmutableSubject_OrgCaseInsensitive combines
// the immutable-ID stripping with the case-insensitive org allowlist match.
func TestVerifyToken_TrustedIssuer_ImmutableSubject_OrgCaseInsensitive(t *testing.T) {
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
