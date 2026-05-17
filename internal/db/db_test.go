package db

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/buildhost/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// --- Projects ----------------------------------------------------------------

func TestCreateAndGetProject(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &model.Project{
		Name:        "myproject",
		Description: "A test project",
		Homepage:    "https://example.com",
		License:     "MIT",
		IsPrivate:   false,
		Versioning:  model.VersioningAuto,
	}
	if err := d.CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("CreateProject did not set ID")
	}

	got, err := d.GetProject(ctx, "myproject")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Name != "myproject" {
		t.Errorf("Name = %q, want %q", got.Name, "myproject")
	}
	if got.Description != "A test project" {
		t.Errorf("Description = %q, want %q", got.Description, "A test project")
	}
	if got.License != "MIT" {
		t.Errorf("License = %q, want %q", got.License, "MIT")
	}
	if got.Versioning != model.VersioningAuto {
		t.Errorf("Versioning = %q, want %q", got.Versioning, model.VersioningAuto)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := d.GetProject(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProject(missing) = %v, want ErrNotFound", err)
	}
}

func TestCreateProjectDuplicateReturnsErrConflict(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &model.Project{Name: "dup", Versioning: model.VersioningAuto}
	if err := d.CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject #1: %v", err)
	}
	p2 := &model.Project{Name: "dup", Versioning: model.VersioningAuto}
	err := d.CreateProject(ctx, p2)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("CreateProject(dup) = %v, want ErrConflict", err)
	}
}

func TestListProjects(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"bravo", "alpha", "charlie"} {
		p := &model.Project{Name: name, Versioning: model.VersioningAuto}
		if err := d.CreateProject(ctx, p); err != nil {
			t.Fatalf("CreateProject(%s): %v", name, err)
		}
	}

	list, err := d.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	// Projects are ordered by name.
	if list[0].Name != "alpha" || list[1].Name != "bravo" || list[2].Name != "charlie" {
		t.Errorf("order = [%s, %s, %s], want [alpha, bravo, charlie]",
			list[0].Name, list[1].Name, list[2].Name)
	}
}

// --- Releases ----------------------------------------------------------------

func createTestProject(t *testing.T, d *DB) *model.Project {
	t.Helper()
	p := &model.Project{Name: "relpkg", Versioning: model.VersioningAuto}
	if err := d.CreateProject(context.Background(), p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p
}

func TestCreateAndGetRelease(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	r := &model.Release{
		ProjectID:  p.ID,
		Version:    "1.0.0",
		VersionNum: 1,
		GitBranch:  "main",
		GitCommit:  "abc123",
		Notes:      "first release",
	}
	if err := d.CreateRelease(ctx, r); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("CreateRelease did not set ID")
	}

	got, err := d.GetRelease(ctx, p.ID, "1.0.0")
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", got.Version, "1.0.0")
	}
	if got.GitBranch != "main" {
		t.Errorf("GitBranch = %q, want %q", got.GitBranch, "main")
	}
	if got.Published {
		t.Error("Published = true on new release, want false")
	}
}

func TestGetReleaseNotFound(t *testing.T) {
	d := openTestDB(t)
	p := createTestProject(t, d)
	_, err := d.GetRelease(context.Background(), p.ID, "9.9.9")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRelease(missing) = %v, want ErrNotFound", err)
	}
}

func TestListReleases(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	for i, v := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		r := &model.Release{
			ProjectID:  p.ID,
			Version:    v,
			VersionNum: int64(i + 1),
		}
		if err := d.CreateRelease(ctx, r); err != nil {
			t.Fatalf("CreateRelease(%s): %v", v, err)
		}
	}

	list, err := d.ListReleases(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	// Ordered by version_num DESC.
	if list[0].Version != "3.0.0" {
		t.Errorf("first release = %q, want 3.0.0", list[0].Version)
	}
}

func TestNextVersionNum(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// No releases yet -- should be 1.
	num, err := d.NextVersionNum(ctx, p.ID)
	if err != nil {
		t.Fatalf("NextVersionNum: %v", err)
	}
	if num != 1 {
		t.Errorf("NextVersionNum(empty) = %d, want 1", num)
	}

	r := &model.Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	if err := d.CreateRelease(ctx, r); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	num, err = d.NextVersionNum(ctx, p.ID)
	if err != nil {
		t.Fatalf("NextVersionNum: %v", err)
	}
	if num != 2 {
		t.Errorf("NextVersionNum(after 1) = %d, want 2", num)
	}
}

func TestPublishRelease(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	r := &model.Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	if err := d.CreateRelease(ctx, r); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	if err := d.PublishRelease(ctx, r.ID); err != nil {
		t.Fatalf("PublishRelease: %v", err)
	}

	got, err := d.GetRelease(ctx, p.ID, "1.0.0")
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	if !got.Published {
		t.Error("Published = false after PublishRelease, want true")
	}
	if got.PublishedAt == nil {
		t.Error("PublishedAt is nil after PublishRelease")
	}
}

func TestGetLatestRelease(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	// No published releases yet.
	_, err := d.GetLatestRelease(ctx, p.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLatestRelease(empty) = %v, want ErrNotFound", err)
	}

	// Create and publish two releases.
	for i, v := range []string{"1.0.0", "2.0.0"} {
		r := &model.Release{ProjectID: p.ID, Version: v, VersionNum: int64(i + 1)}
		if err := d.CreateRelease(ctx, r); err != nil {
			t.Fatalf("CreateRelease(%s): %v", v, err)
		}
		if err := d.PublishRelease(ctx, r.ID); err != nil {
			t.Fatalf("PublishRelease(%s): %v", v, err)
		}
	}

	got, err := d.GetLatestRelease(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetLatestRelease: %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("latest = %q, want 2.0.0", got.Version)
	}
}

func TestGetLatestReleaseByBranch(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	p := createTestProject(t, d)

	releases := []struct {
		version string
		num     int64
		branch  string
	}{
		{"1.0.0", 1, "main"},
		{"2.0.0", 2, "main"},
		{"3.0.0-rc1", 3, "develop"},
	}
	for _, rl := range releases {
		r := &model.Release{
			ProjectID:  p.ID,
			Version:    rl.version,
			VersionNum: rl.num,
			GitBranch:  rl.branch,
		}
		if err := d.CreateRelease(ctx, r); err != nil {
			t.Fatalf("CreateRelease(%s): %v", rl.version, err)
		}
		if err := d.PublishRelease(ctx, r.ID); err != nil {
			t.Fatalf("PublishRelease(%s): %v", rl.version, err)
		}
	}

	got, err := d.GetLatestReleaseByBranch(ctx, p.ID, "main")
	if err != nil {
		t.Fatalf("GetLatestReleaseByBranch(main): %v", err)
	}
	if got.Version != "2.0.0" {
		t.Errorf("latest main = %q, want 2.0.0", got.Version)
	}

	got, err = d.GetLatestReleaseByBranch(ctx, p.ID, "develop")
	if err != nil {
		t.Fatalf("GetLatestReleaseByBranch(develop): %v", err)
	}
	if got.Version != "3.0.0-rc1" {
		t.Errorf("latest develop = %q, want 3.0.0-rc1", got.Version)
	}

	_, err = d.GetLatestReleaseByBranch(ctx, p.ID, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLatestReleaseByBranch(nonexistent) = %v, want ErrNotFound", err)
	}
}

// --- Artifacts ---------------------------------------------------------------

func createTestRelease(t *testing.T, d *DB) (*model.Project, *model.Release) {
	t.Helper()
	p := createTestProject(t, d)
	r := &model.Release{ProjectID: p.ID, Version: "1.0.0", VersionNum: 1}
	if err := d.CreateRelease(context.Background(), r); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	return p, r
}

func TestCreateAndGetArtifact(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &model.Artifact{
		ReleaseID:  r.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindBinary,
		StorageKey: "deadbeef",
		Size:       1024,
		SHA256:     "aabbccdd",
		Filename:   "mybin",
	}
	if err := d.CreateArtifact(ctx, a); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("CreateArtifact did not set ID")
	}

	got, err := d.GetArtifact(ctx, r.ID, string(model.OSLinux), string(model.ArchAMD64))
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if got.StorageKey != "deadbeef" {
		t.Errorf("StorageKey = %q, want %q", got.StorageKey, "deadbeef")
	}
	if got.Size != 1024 {
		t.Errorf("Size = %d, want 1024", got.Size)
	}
	if got.Filename != "mybin" {
		t.Errorf("Filename = %q, want %q", got.Filename, "mybin")
	}
}

func TestGetArtifactNotFound(t *testing.T) {
	d := openTestDB(t)
	_, r := createTestRelease(t, d)
	_, err := d.GetArtifact(context.Background(), r.ID, "linux", "amd64")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetArtifact(missing) = %v, want ErrNotFound", err)
	}
}

func TestListArtifacts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	artifacts := []struct {
		os   model.OS
		arch model.Arch
	}{
		{model.OSLinux, model.ArchAMD64},
		{model.OSLinux, model.ArchARM64},
		{model.OSDarwin, model.ArchAMD64},
	}
	for _, art := range artifacts {
		a := &model.Artifact{
			ReleaseID:  r.ID,
			OS:         art.os,
			Arch:       art.arch,
			Kind:       model.KindBinary,
			StorageKey: "key",
			Size:       100,
			SHA256:     "hash",
		}
		if err := d.CreateArtifact(ctx, a); err != nil {
			t.Fatalf("CreateArtifact(%s/%s): %v", art.os, art.arch, err)
		}
	}

	list, err := d.ListArtifacts(ctx, r.ID)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("len = %d, want 3", len(list))
	}
}

func TestUpdateArtifactStripped(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &model.Artifact{
		ReleaseID:  r.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindBinary,
		StorageKey: "orig-key",
		Size:       2048,
		SHA256:     "origsha",
	}
	if err := d.CreateArtifact(ctx, a); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	if err := d.UpdateArtifactStripped(ctx, a.ID, "strip-key", 1024, "stripsha", "dbg-key", 512); err != nil {
		t.Fatalf("UpdateArtifactStripped: %v", err)
	}

	got, err := d.GetArtifact(ctx, r.ID, string(model.OSLinux), string(model.ArchAMD64))
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if got.StrippedStorageKey != "strip-key" {
		t.Errorf("StrippedStorageKey = %q, want %q", got.StrippedStorageKey, "strip-key")
	}
	if got.StrippedSize != 1024 {
		t.Errorf("StrippedSize = %d, want 1024", got.StrippedSize)
	}
	if got.StrippedSHA256 != "stripsha" {
		t.Errorf("StrippedSHA256 = %q, want %q", got.StrippedSHA256, "stripsha")
	}
	if got.DebugStorageKey != "dbg-key" {
		t.Errorf("DebugStorageKey = %q, want %q", got.DebugStorageKey, "dbg-key")
	}
	if got.DebugSize != 512 {
		t.Errorf("DebugSize = %d, want 512", got.DebugSize)
	}
}

// --- Packaged Artifacts ------------------------------------------------------

func TestCreateAndGetPackagedArtifact(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &model.Artifact{
		ReleaseID:  r.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindBinary,
		StorageKey: "binkey",
		Size:       500,
		SHA256:     "binhash",
	}
	if err := d.CreateArtifact(ctx, a); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	if err := d.CreatePackagedArtifact(ctx, a.ID, "deb", "debkey", 600, "debhash", "pkg.deb", `{"arch":"amd64"}`); err != nil {
		t.Fatalf("CreatePackagedArtifact: %v", err)
	}

	key, size, sha, filename, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	if err != nil {
		t.Fatalf("GetPackagedArtifact: %v", err)
	}
	if key != "debkey" {
		t.Errorf("StorageKey = %q, want %q", key, "debkey")
	}
	if size != 600 {
		t.Errorf("Size = %d, want 600", size)
	}
	if sha != "debhash" {
		t.Errorf("SHA256 = %q, want %q", sha, "debhash")
	}
	if filename != "pkg.deb" {
		t.Errorf("Filename = %q, want %q", filename, "pkg.deb")
	}
}

func TestGetPackagedArtifactNotFound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &model.Artifact{
		ReleaseID:  r.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindBinary,
		StorageKey: "k",
		Size:       1,
		SHA256:     "h",
	}
	if err := d.CreateArtifact(ctx, a); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	_, _, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "rpm")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPackagedArtifact(missing) = %v, want ErrNotFound", err)
	}
}

func TestCreatePackagedArtifactUpserts(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	_, r := createTestRelease(t, d)

	a := &model.Artifact{
		ReleaseID:  r.ID,
		OS:         model.OSLinux,
		Arch:       model.ArchAMD64,
		Kind:       model.KindBinary,
		StorageKey: "k",
		Size:       1,
		SHA256:     "h",
	}
	if err := d.CreateArtifact(ctx, a); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	// Insert, then replace with different values.
	if err := d.CreatePackagedArtifact(ctx, a.ID, "deb", "key1", 100, "sha1", "f1.deb", "{}"); err != nil {
		t.Fatalf("CreatePackagedArtifact #1: %v", err)
	}
	if err := d.CreatePackagedArtifact(ctx, a.ID, "deb", "key2", 200, "sha2", "f2.deb", "{}"); err != nil {
		t.Fatalf("CreatePackagedArtifact #2: %v", err)
	}

	key, size, _, _, err := d.GetPackagedArtifact(ctx, a.ID, "deb")
	if err != nil {
		t.Fatalf("GetPackagedArtifact: %v", err)
	}
	if key != "key2" {
		t.Errorf("StorageKey = %q, want %q (upsert)", key, "key2")
	}
	if size != 200 {
		t.Errorf("Size = %d, want 200 (upsert)", size)
	}
}

// --- Tokens ------------------------------------------------------------------

func TestCreateAndLookupToken(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "ci-token", nil, "read,write")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	if !strings.HasPrefix(plaintext, "bh_") {
		t.Errorf("plaintext prefix = %q, want bh_", plaintext[:4])
	}
	if tok.ID == 0 {
		t.Fatal("CreateToken did not set ID")
	}
	if tok.Name != "ci-token" {
		t.Errorf("Name = %q, want %q", tok.Name, "ci-token")
	}
	if tok.Scopes != "read,write" {
		t.Errorf("Scopes = %q, want %q", tok.Scopes, "read,write")
	}

	looked, err := d.LookupToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("LookupToken: %v", err)
	}
	if looked.ID != tok.ID {
		t.Errorf("LookupToken ID = %d, want %d", looked.ID, tok.ID)
	}
	if looked.Name != "ci-token" {
		t.Errorf("LookupToken Name = %q, want %q", looked.Name, "ci-token")
	}
}

func TestLookupTokenNotFound(t *testing.T) {
	d := openTestDB(t)
	_, err := d.LookupToken(context.Background(), "bh_bogus_token_value_here")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("LookupToken(invalid) = %v, want ErrNotFound", err)
	}
}

func TestCreateTokenWithProjectScope(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	p := &model.Project{Name: "scoped", Versioning: model.VersioningAuto}
	if err := d.CreateProject(ctx, p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	pid := p.ID
	_, tok, err := d.CreateToken(ctx, "proj-token", &pid, "read")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if tok.ProjectID == nil || *tok.ProjectID != p.ID {
		t.Errorf("ProjectID = %v, want %d", tok.ProjectID, p.ID)
	}
}

func TestListTokens(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	for _, name := range []string{"token-a", "token-b", "token-c"} {
		if _, _, err := d.CreateToken(ctx, name, nil, "read"); err != nil {
			t.Fatalf("CreateToken(%s): %v", name, err)
		}
	}

	list, err := d.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
}

func TestDeleteToken(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	plaintext, tok, err := d.CreateToken(ctx, "doomed", nil, "read")
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	if err := d.DeleteToken(ctx, tok.ID); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	_, err = d.LookupToken(ctx, plaintext)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("LookupToken(deleted) = %v, want ErrNotFound", err)
	}
}

func TestDeleteTokenNotFound(t *testing.T) {
	d := openTestDB(t)
	err := d.DeleteToken(context.Background(), 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteToken(missing) = %v, want ErrNotFound", err)
	}
}
