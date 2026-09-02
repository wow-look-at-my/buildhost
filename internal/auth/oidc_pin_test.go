package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/buildhost/internal/db"
)

// These cover the GitHub repo-identity pin (projects.github_owner_id /

const (
	pinOwnerID = "250878655"
	pinRepoID  = "1307105896"
)

// pinTestCtx builds the context an OIDC-authenticated request carries: the
// synthetic token, the namespace authorization, and the repo identity. The
// issuer is deliberately NOT GitHubActionsIssuer so the default-branch GitHub
// lookup stays out of these tests.
func pinTestCtx(ownerID, repoID string) context.Context {
	ctx := WithToken(context.Background(), &db.APIToken{ID: -1, Name: "oidc:test", Scopes: "read,write"})
	ctx = WithOIDCProject(ctx, "tesla-wheel-data")
	ctx = WithOIDCPrivate(ctx, false)
	return WithOIDCRepo(ctx, OIDCRepoIdentity{
		RepoPath: "wow-look-at-my/tesla-wheel-data",
		Issuer:   "https://token.test",
		OwnerID:  ownerID,
		RepoID:   repoID,
	})
}

func pinTestRequest(t *testing.T, access AccessLevel, ownerID, repoID string) *httptest.ResponseRecorder {
	t.Helper()
	parse := func(r *http.Request) RouteInfo {
		return testRouteInfo{project: "tesla-wheel-data", access: access}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	handler := requireProjectFunc(parse, inner)

	method := "GET"
	if access == WriteAccess {
		method = "POST"
	}
	req := httptest.NewRequest(method, "/api/v1/projects/tesla-wheel-data/releases", nil)
	req = req.WithContext(pinTestCtx(ownerID, repoID))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// Provisioning a new project from an ID-bearing token pins the IDs from birth.
func TestOIDCPin_ProvisionPinsIDs(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	rec := pinTestRequest(t, WriteAccess, pinOwnerID, pinRepoID)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	created, err := d.GetProject(context.Background(), "tesla-wheel-data")
	require.NoError(t, err)
	assert.Equal(t, pinOwnerID, created.GithubOwnerID)
	assert.Equal(t, pinRepoID, created.GithubRepoID)
	assert.Equal(t, "wow-look-at-my/tesla-wheel-data", created.GithubRepo)
}

// A pre-existing project without pinned IDs (provisioned before the pin
func TestOIDCPin_LegacyProject_FirstIDPublishPins(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "tesla-wheel-data", Versioning: db.VersioningAuto, GithubRepo: "wow-look-at-my/tesla-wheel-data"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, WriteAccess, pinOwnerID, pinRepoID)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	got, err := d.GetProject(context.Background(), "tesla-wheel-data")
	require.NoError(t, err)
	assert.Equal(t, pinOwnerID, got.GithubOwnerID)
	assert.Equal(t, pinRepoID, got.GithubRepoID)
}

// A read never pins (mirrors write-only provisioning: reads don't mutate).
func TestOIDCPin_ReadDoesNotPin(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "tesla-wheel-data", Versioning: db.VersioningAuto, GithubRepo: "wow-look-at-my/tesla-wheel-data"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, ReadAccess, pinOwnerID, pinRepoID)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	got, err := d.GetProject(context.Background(), "tesla-wheel-data")
	require.NoError(t, err)
	assert.Empty(t, got.GithubOwnerID)
	assert.Empty(t, got.GithubRepoID)
}

// A pinned project accepts later publishes carrying the same IDs.
func TestOIDCPin_MatchingIDs_Allowed(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{
		Name: "tesla-wheel-data", Versioning: db.VersioningAuto,
		GithubRepo: "wow-look-at-my/tesla-wheel-data", GithubOwnerID: pinOwnerID, GithubRepoID: pinRepoID,
	}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, WriteAccess, pinOwnerID, pinRepoID)
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

// The resurrection case: same names, different IDs. The publish is refused
// loudly, naming both identities and the reason.
func TestOIDCPin_MismatchedIDs_WriteRejected(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{
		Name: "tesla-wheel-data", Versioning: db.VersioningAuto,
		GithubRepo: "wow-look-at-my/tesla-wheel-data", GithubOwnerID: pinOwnerID, GithubRepoID: pinRepoID,
	}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, WriteAccess, pinOwnerID, "424242")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "OIDC repo identity mismatch")
	assert.Contains(t, rec.Body.String(), "resurrected")

	// The pin itself is untouched.
	got, err := d.GetProject(context.Background(), "tesla-wheel-data")
	require.NoError(t, err)
	assert.Equal(t, pinRepoID, got.GithubRepoID)
}

// Mismatched IDs are refused on reads too -- a resurrected repo must not read
// a private predecessor's artifacts either.
func TestOIDCPin_MismatchedIDs_ReadRejected(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{
		Name: "tesla-wheel-data", Versioning: db.VersioningAuto, IsPrivate: true,
		GithubRepo: "wow-look-at-my/tesla-wheel-data", GithubOwnerID: pinOwnerID, GithubRepoID: pinRepoID,
	}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, ReadAccess, "777", pinRepoID)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "OIDC repo identity mismatch")
}

func TestOIDCPin_MismatchedIDs_HiddenReadGets404(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{
		Name: "tesla-wheel-data", Versioning: db.VersioningAuto, IsPrivate: true,
		GithubRepo: "wow-look-at-my/tesla-wheel-data", GithubOwnerID: pinOwnerID, GithubRepoID: pinRepoID,
	}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, HiddenReadAccess, "777", "888")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":"project not found"}`, rec.Body.String())
}

// A token without IDs (an issuer that mints neither the repository_id claims
// nor immutable subjects) is deliberately NOT rejected by the pin: GitHub
// controls which format a repo's tokens get, and the token already passed the
// issuer/org/event gates. Documented allow.
func TestOIDCPin_PinnedProject_IDLessTokenAllowed(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{
		Name: "tesla-wheel-data", Versioning: db.VersioningAuto,
		GithubRepo: "wow-look-at-my/tesla-wheel-data", GithubOwnerID: pinOwnerID, GithubRepoID: pinRepoID,
	}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, WriteAccess, "", "")
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

// An ID-less token against an unpinned legacy project keeps working and pins
// nothing (there is nothing to pin).
func TestOIDCPin_LegacyProject_IDLessTokenAllowed_NoPin(t *testing.T) {
	d := openTestDB(t)
	initTestMiddleware(t, d)

	proj := &db.Project{Name: "tesla-wheel-data", Versioning: db.VersioningAuto, GithubRepo: "wow-look-at-my/tesla-wheel-data"}
	require.NoError(t, d.CreateProject(context.Background(), proj))

	rec := pinTestRequest(t, WriteAccess, "", "")
	assert.Equal(t, http.StatusAccepted, rec.Code)

	got, err := d.GetProject(context.Background(), "tesla-wheel-data")
	require.NoError(t, err)
	assert.Empty(t, got.GithubOwnerID)
	assert.Empty(t, got.GithubRepoID)
}
