package db

import (
	"context"
	"errors"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestOIDCPolicy_CRUD(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Create a policy.
	p := &model.OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read,write",
	}
	require.NoError(t, d.CreateOIDCPolicy(ctx, p))
	assert.NotEqual(t, int64(0), p.ID)

	// List policies should return the one we created.
	policies, err := d.ListOIDCPolicies(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, len(policies))
	assert.Equal(t, p.ID, policies[0].ID)
	assert.Equal(t, "https://token.actions.githubusercontent.com", policies[0].Issuer)
	assert.Equal(t, "repo:myorg/myrepo:*", policies[0].SubjectPattern)
	assert.Equal(t, "read,write", policies[0].Scopes)
	assert.Nil(t, policies[0].ProjectID)

	// Delete it.
	require.NoError(t, d.DeleteOIDCPolicy(ctx, p.ID))

	// List should now be empty.
	policies, err = d.ListOIDCPolicies(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, len(policies))
}

func TestOIDCPolicy_CreateDuplicateReturnsErrConflict(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p1 := &model.OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "read",
	}
	require.NoError(t, d.CreateOIDCPolicy(ctx, p1))

	// Duplicate issuer+subject_pattern should return ErrConflict.
	p2 := &model.OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:myorg/myrepo:*",
		Scopes:         "write",
	}
	err := d.CreateOIDCPolicy(ctx, p2)
	assert.True(t, errors.Is(err, ErrConflict))
}

func TestOIDCPolicy_DeleteNotFoundReturnsErrNotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	err := d.DeleteOIDCPolicy(ctx, 99999)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestOIDCPolicy_WithProjectScope(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Create a project to scope the policy to.
	proj := &model.Project{Name: "scoped", Versioning: model.VersioningAuto}
	require.NoError(t, d.CreateProject(ctx, proj))

	pid := proj.ID
	p := &model.OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:myorg/scoped:*",
		ProjectID:      &pid,
		Scopes:         "read",
	}
	require.NoError(t, d.CreateOIDCPolicy(ctx, p))

	// Verify it round-trips with the project ID.
	got, err := d.GetOIDCPolicyByID(ctx, p.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ProjectID)
	assert.Equal(t, proj.ID, *got.ProjectID)
	assert.Equal(t, "read", got.Scopes)
}

func TestOIDCPolicy_ListByIssuer(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	policies := []struct {
		issuer, sub string
	}{
		{"https://issuer-a.example.com", "sub:repo-1"},
		{"https://issuer-b.example.com", "sub:repo-2"},
		{"https://issuer-a.example.com", "sub:repo-3"},
	}
	for _, entry := range policies {
		p := &model.OIDCPolicy{
			Issuer:         entry.issuer,
			SubjectPattern: entry.sub,
			Scopes:         "read",
		}
		require.NoError(t, d.CreateOIDCPolicy(ctx, p))
	}

	// List by issuer A should return 2.
	policiesA, err := d.ListOIDCPoliciesByIssuer(ctx, "https://issuer-a.example.com")
	require.NoError(t, err)
	assert.Equal(t, 2, len(policiesA))

	// List by issuer B should return 1.
	policiesB, err := d.ListOIDCPoliciesByIssuer(ctx, "https://issuer-b.example.com")
	require.NoError(t, err)
	assert.Equal(t, 1, len(policiesB))

	// List by non-existent issuer should return empty.
	policiesC, err := d.ListOIDCPoliciesByIssuer(ctx, "https://nonexistent.example.com")
	require.NoError(t, err)
	assert.Equal(t, 0, len(policiesC))
}

func TestOIDCPolicy_GetByID_NotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	_, err := d.GetOIDCPolicyByID(ctx, 99999)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestOIDCPolicy_DifferentSubjectPatternAllowed(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Same issuer but different subject_pattern should succeed.
	p1 := &model.OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:myorg/repo-a:*",
		Scopes:         "read",
	}
	require.NoError(t, d.CreateOIDCPolicy(ctx, p1))

	p2 := &model.OIDCPolicy{
		Issuer:         "https://token.actions.githubusercontent.com",
		SubjectPattern: "repo:myorg/repo-b:*",
		Scopes:         "read,write",
	}
	require.NoError(t, d.CreateOIDCPolicy(ctx, p2))

	policies, err := d.ListOIDCPolicies(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, len(policies))
}
