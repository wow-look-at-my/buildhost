package version

import (
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/stretchr/testify/require"
)

func TestResolve_AutoVersioned_ExactMatch(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningAuto}
	releases := []db.Release{
		{ID: 3, VersionNum: 3, Version: "3"},
		{ID: 2, VersionNum: 2, Version: "2"},
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	got, err := Resolve(nil, project, "2", releases)
	require.Nil(t, err)

	require.Equal(t, int64(2), got.VersionNum)

}

func TestResolve_AutoVersioned_Latest(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningAuto}
	releases := []db.Release{
		{ID: 3, VersionNum: 3, Version: "3"},
		{ID: 2, VersionNum: 2, Version: "2"},
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	got, err := Resolve(nil, project, "latest", releases)
	require.Nil(t, err)

	require.Equal(t, int64(3), got.VersionNum)

}

func TestResolve_AutoVersioned_EmptySpec(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningAuto}
	releases := []db.Release{
		{ID: 5, VersionNum: 5, Version: "5"},
	}

	got, err := Resolve(nil, project, "", releases)
	require.Nil(t, err)

	require.Equal(t, int64(5), got.VersionNum)

}

func TestResolve_AutoVersioned_NotFound(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningAuto}
	releases := []db.Release{
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	_, err := Resolve(nil, project, "99", releases)
	require.NotNil(t, err)

}

func TestResolve_AutoVersioned_InvalidSpec(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningAuto}
	releases := []db.Release{
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	_, err := Resolve(nil, project, "not-a-number", releases)
	require.NotNil(t, err)

}

func TestResolve_Semver_ExactMatch(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 3, Version: "1.2.0", VersionNum: 1002000},
		{ID: 2, Version: "1.1.0", VersionNum: 1001000},
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	got, err := Resolve(nil, project, "1.1.0", releases)
	require.Nil(t, err)

	require.Equal(t, "1.1.0", got.Version)

}

func TestResolve_Semver_ExactMatchWithVPrefix(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 2, Version: "v2.0.0", VersionNum: 2000000},
		{ID: 1, Version: "v1.0.0", VersionNum: 1000000},
	}

	got, err := Resolve(nil, project, "v1.0.0", releases)
	require.Nil(t, err)

	require.Equal(t, int64(1), got.ID)

}

func TestResolve_Semver_MajorPrefix(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 4, Version: "2.1.0", VersionNum: 2001000},
		{ID: 3, Version: "1.3.0", VersionNum: 1003000},
		{ID: 2, Version: "1.2.0", VersionNum: 1002000},
		{ID: 1, Version: "1.1.0", VersionNum: 1001000},
	}

	// "1" should match the first (highest) release starting with "1."
	got, err := Resolve(nil, project, "1", releases)
	require.Nil(t, err)

	require.Equal(t, "1.3.0", got.Version)

}

func TestResolve_Semver_MajorMinorPrefix(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 4, Version: "1.2.3", VersionNum: 1002003},
		{ID: 3, Version: "1.2.1", VersionNum: 1002001},
		{ID: 2, Version: "1.1.9", VersionNum: 1001009},
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	// "1.2" should match the first (highest) release starting with "1.2."
	got, err := Resolve(nil, project, "1.2", releases)
	require.Nil(t, err)

	require.Equal(t, "1.2.3", got.Version)

}

func TestResolve_Semver_SkipsPrerelease(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 3, Version: "1.3.0-rc1", VersionNum: 1003000},
		{ID: 2, Version: "1.2.0", VersionNum: 1002000},
		{ID: 1, Version: "1.1.0", VersionNum: 1001000},
	}

	// "1" prefix should skip the prerelease and land on 1.2.0
	got, err := Resolve(nil, project, "1", releases)
	require.Nil(t, err)

	require.Equal(t, "1.2.0", got.Version)

}

func TestResolve_Semver_NotFound(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	_, err := Resolve(nil, project, "9.9.9", releases)
	require.NotNil(t, err)

}

func TestResolve_Semver_Latest(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}
	releases := []db.Release{
		{ID: 2, Version: "2.0.0", VersionNum: 2000000},
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	got, err := Resolve(nil, project, "latest", releases)
	require.Nil(t, err)

	require.Equal(t, "2.0.0", got.Version)

}

func TestResolve_EmptyReleases(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningAuto}

	_, err := Resolve(nil, project, "1", nil)
	require.NotNil(t, err)

	_, err = Resolve(nil, project, "latest", []db.Release{})
	require.NotNil(t, err)

}

func TestResolve_EmptyReleases_Semver(t *testing.T) {
	project := &db.Project{ID: 1, Versioning: db.VersioningSemver}

	_, err := Resolve(nil, project, "1.0.0", nil)
	require.NotNil(t, err)

}
