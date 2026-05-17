package version

import (
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func TestResolve_AutoVersioned_ExactMatch(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningAuto}
	releases := []model.Release{
		{ID: 3, VersionNum: 3, Version: "3"},
		{ID: 2, VersionNum: 2, Version: "2"},
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	got, err := Resolve(nil, project, "2", releases)
	if err != nil {
		t.Fatalf("Resolve auto exact: %v", err)
	}
	if got.VersionNum != 2 {
		t.Fatalf("Resolve auto exact: got VersionNum=%d, want 2", got.VersionNum)
	}
}

func TestResolve_AutoVersioned_Latest(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningAuto}
	releases := []model.Release{
		{ID: 3, VersionNum: 3, Version: "3"},
		{ID: 2, VersionNum: 2, Version: "2"},
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	got, err := Resolve(nil, project, "latest", releases)
	if err != nil {
		t.Fatalf("Resolve auto latest: %v", err)
	}
	if got.VersionNum != 3 {
		t.Fatalf("Resolve auto latest: got VersionNum=%d, want 3", got.VersionNum)
	}
}

func TestResolve_AutoVersioned_EmptySpec(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningAuto}
	releases := []model.Release{
		{ID: 5, VersionNum: 5, Version: "5"},
	}

	got, err := Resolve(nil, project, "", releases)
	if err != nil {
		t.Fatalf("Resolve auto empty spec: %v", err)
	}
	if got.VersionNum != 5 {
		t.Fatalf("Resolve auto empty spec: got VersionNum=%d, want 5", got.VersionNum)
	}
}

func TestResolve_AutoVersioned_NotFound(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningAuto}
	releases := []model.Release{
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	_, err := Resolve(nil, project, "99", releases)
	if err == nil {
		t.Fatal("Resolve auto not found: expected error, got nil")
	}
}

func TestResolve_AutoVersioned_InvalidSpec(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningAuto}
	releases := []model.Release{
		{ID: 1, VersionNum: 1, Version: "1"},
	}

	_, err := Resolve(nil, project, "not-a-number", releases)
	if err == nil {
		t.Fatal("Resolve auto invalid: expected error, got nil")
	}
}

func TestResolve_Semver_ExactMatch(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 3, Version: "1.2.0", VersionNum: 1002000},
		{ID: 2, Version: "1.1.0", VersionNum: 1001000},
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	got, err := Resolve(nil, project, "1.1.0", releases)
	if err != nil {
		t.Fatalf("Resolve semver exact: %v", err)
	}
	if got.Version != "1.1.0" {
		t.Fatalf("Resolve semver exact: got %q, want %q", got.Version, "1.1.0")
	}
}

func TestResolve_Semver_ExactMatchWithVPrefix(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 2, Version: "v2.0.0", VersionNum: 2000000},
		{ID: 1, Version: "v1.0.0", VersionNum: 1000000},
	}

	got, err := Resolve(nil, project, "v1.0.0", releases)
	if err != nil {
		t.Fatalf("Resolve semver v-prefix: %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("Resolve semver v-prefix: got ID=%d, want 1", got.ID)
	}
}

func TestResolve_Semver_MajorPrefix(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 4, Version: "2.1.0", VersionNum: 2001000},
		{ID: 3, Version: "1.3.0", VersionNum: 1003000},
		{ID: 2, Version: "1.2.0", VersionNum: 1002000},
		{ID: 1, Version: "1.1.0", VersionNum: 1001000},
	}

	// "1" should match the first (highest) release starting with "1."
	got, err := Resolve(nil, project, "1", releases)
	if err != nil {
		t.Fatalf("Resolve semver major: %v", err)
	}
	if got.Version != "1.3.0" {
		t.Fatalf("Resolve semver major: got %q, want %q", got.Version, "1.3.0")
	}
}

func TestResolve_Semver_MajorMinorPrefix(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 4, Version: "1.2.3", VersionNum: 1002003},
		{ID: 3, Version: "1.2.1", VersionNum: 1002001},
		{ID: 2, Version: "1.1.9", VersionNum: 1001009},
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	// "1.2" should match the first (highest) release starting with "1.2."
	got, err := Resolve(nil, project, "1.2", releases)
	if err != nil {
		t.Fatalf("Resolve semver major.minor: %v", err)
	}
	if got.Version != "1.2.3" {
		t.Fatalf("Resolve semver major.minor: got %q, want %q", got.Version, "1.2.3")
	}
}

func TestResolve_Semver_SkipsPrerelease(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 3, Version: "1.3.0-rc1", VersionNum: 1003000},
		{ID: 2, Version: "1.2.0", VersionNum: 1002000},
		{ID: 1, Version: "1.1.0", VersionNum: 1001000},
	}

	// "1" prefix should skip the prerelease and land on 1.2.0
	got, err := Resolve(nil, project, "1", releases)
	if err != nil {
		t.Fatalf("Resolve semver skip prerelease: %v", err)
	}
	if got.Version != "1.2.0" {
		t.Fatalf("Resolve semver skip prerelease: got %q, want %q", got.Version, "1.2.0")
	}
}

func TestResolve_Semver_NotFound(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	_, err := Resolve(nil, project, "9.9.9", releases)
	if err == nil {
		t.Fatal("Resolve semver not found: expected error, got nil")
	}
}

func TestResolve_Semver_Latest(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}
	releases := []model.Release{
		{ID: 2, Version: "2.0.0", VersionNum: 2000000},
		{ID: 1, Version: "1.0.0", VersionNum: 1000000},
	}

	got, err := Resolve(nil, project, "latest", releases)
	if err != nil {
		t.Fatalf("Resolve semver latest: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Fatalf("Resolve semver latest: got %q, want %q", got.Version, "2.0.0")
	}
}

func TestResolve_EmptyReleases(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningAuto}

	_, err := Resolve(nil, project, "1", nil)
	if err == nil {
		t.Fatal("Resolve empty releases: expected error, got nil")
	}

	_, err = Resolve(nil, project, "latest", []model.Release{})
	if err == nil {
		t.Fatal("Resolve empty slice: expected error, got nil")
	}
}

func TestResolve_EmptyReleases_Semver(t *testing.T) {
	project := &model.Project{ID: 1, Versioning: model.VersioningSemver}

	_, err := Resolve(nil, project, "1.0.0", nil)
	if err == nil {
		t.Fatal("Resolve semver empty releases: expected error, got nil")
	}
}
