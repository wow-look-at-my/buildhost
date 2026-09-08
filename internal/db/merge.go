package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/wow-look-at-my/go-containers/set"
	"strconv"
)

// MergeRelease is a release moving to the target, with the version it takes
// there. Un-renumbered it would collide on UNIQUE(project_id, version).
type MergeRelease struct {
	ID         int64
	OldVersion string
	NewVersion string
	OldNum     int64
	NewNum     int64
}

// ProjectMergePlan is the complete set of changes a merge makes. A plan with a
// non-empty Conflicts is not applicable.
type ProjectMergePlan struct {
	From      *Project
	Into      *Project
	Releases  []MergeRelease
	Aliases   []string
	Sites     int
	OCITags   int
	OCIBlobs  int
	DupBlobs  []string
	Tokens    int
	Policies  int
	Conflicts []string
}

// Applicable reports whether the plan can run.
func (p *ProjectMergePlan) Applicable() bool { return len(p.Conflicts) == 0 }

// PlanProjectMerge computes the merge of from into into. It reads only.
func (d *DB) PlanProjectMerge(ctx context.Context, from, into string) (*ProjectMergePlan, error) {
	if from == into {
		return nil, fmt.Errorf("merge source and target are the same project %q", from)
	}
	src, err := d.GetProject(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("merge source %q: %w", from, err)
	}
	dst, err := d.GetProject(ctx, into)
	if err != nil {
		return nil, fmt.Errorf("merge target %q: %w", into, err)
	}

	plan := &ProjectMergePlan{From: src, Into: dst}

	// A shared repo id is the whole reason these are the same project. Without
	// it this is a different operation, refused rather than made a flag.
	switch {
	case src.GithubRepoID == "" || dst.GithubRepoID == "":
		plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
			"one side has no pinned GitHub repo id (%q=%q, %q=%q); merge only joins projects proven to be the same repo",
			src.Name, src.GithubRepoID, dst.Name, dst.GithubRepoID))
	case src.GithubRepoID != dst.GithubRepoID:
		plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
			"different GitHub repos: %q is repo id %s, %q is repo id %s",
			src.Name, src.GithubRepoID, dst.Name, dst.GithubRepoID))
	}

	maxNum, err := d.q.MaxReleaseVersionNum(ctx, dst.ID)
	if err != nil {
		return nil, fmt.Errorf("max version of %q: %w", dst.Name, err)
	}
	rows, err := d.q.ListReleasesForMerge(ctx, src.ID)
	if err != nil {
		return nil, fmt.Errorf("releases of %q: %w", src.Name, err)
	}
	next := maxNum
	for _, r := range rows {
		next++
		mr := MergeRelease{ID: r.ID, OldVersion: r.Version, OldNum: r.VersionNum, NewNum: next}
		// An auto-versioned string IS its number, so renumbering rewrites it.
		// A semver string is meaningful and kept; only its ordering moves.
		if dst.Versioning == VersioningSemver || src.Versioning == VersioningSemver {
			mr.NewVersion = r.Version
			if taken, err := d.versionTaken(ctx, dst.ID, r.Version); err != nil {
				return nil, err
			} else if taken {
				plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
					"version %q exists in both %q and %q; a semver merge cannot rename it",
					r.Version, src.Name, dst.Name))
			}
		} else {
			mr.NewVersion = strconv.FormatInt(next, 10)
		}
		plan.Releases = append(plan.Releases, mr)
	}

	if err := d.planSideTables(ctx, plan, src, dst); err != nil {
		return nil, err
	}

	aliases, err := d.q.ListProjectAliases(ctx, src.ID)
	if err != nil {
		return nil, fmt.Errorf("aliases of %q: %w", src.Name, err)
	}
	plan.Aliases = append(append([]string{}, aliases...), src.Name)
	return plan, nil
}

// planSideTables counts what moves beside the releases and records collisions.
// download_counts and download_events key off artifact_id, so they follow.
func (d *DB) planSideTables(ctx context.Context, plan *ProjectMergePlan, src, dst *Project) error {
	srcSites, err := d.q.ListSitesByProject(ctx, src.ID)
	if err != nil {
		return fmt.Errorf("sites of %q: %w", src.Name, err)
	}
	dstSites, err := d.q.ListSitesByProject(ctx, dst.ID)
	if err != nil {
		return fmt.Errorf("sites of %q: %w", dst.Name, err)
	}
	dstBranch := set.New[string]()
	for _, s := range dstSites {
		dstBranch.Add(s.Branch)
	}
	for _, s := range srcSites {
		if dstBranch.Contains(s.Branch) {
			plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
				"both projects deploy a site for branch %q; keeping one would silently drop the other", s.Branch))
		}
	}
	plan.Sites = len(srcSites)

	srcTags, err := d.q.ListOCITags(ctx, src.ID)
	if err != nil {
		return fmt.Errorf("oci tags of %q: %w", src.Name, err)
	}
	dstTags, err := d.q.ListOCITags(ctx, dst.ID)
	if err != nil {
		return fmt.Errorf("oci tags of %q: %w", dst.Name, err)
	}
	dstTag := set.New[string]()
	for _, t := range dstTags {
		dstTag.Add(t.Tag)
	}
	for _, t := range srcTags {
		if dstTag.Contains(t.Tag) {
			plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
				"both projects publish the OCI tag %q; keeping one would silently drop the other", t.Tag))
		}
	}
	plan.OCITags = len(srcTags)

	// A blob link is a grant, not content: a shared key is the same bytes.
	srcKeys, err := d.q.ListOCIBlobKeys(ctx, src.ID)
	if err != nil {
		return fmt.Errorf("oci blobs of %q: %w", src.Name, err)
	}
	dstKeys, err := d.q.ListOCIBlobKeys(ctx, dst.ID)
	if err != nil {
		return fmt.Errorf("oci blobs of %q: %w", dst.Name, err)
	}
	dstKey := set.New[string]()
	for _, k := range dstKeys {
		dstKey.Add(k)
	}
	for _, k := range srcKeys {
		if dstKey.Contains(k) {
			plan.DupBlobs = append(plan.DupBlobs, k)
		}
	}
	plan.OCIBlobs = len(srcKeys) - len(plan.DupBlobs)

	if plan.Tokens, err = d.countRows(ctx, "SELECT COUNT(*) FROM api_tokens WHERE project_id = ?", src.ID); err != nil {
		return err
	}
	if plan.Policies, err = d.countRows(ctx, "SELECT COUNT(*) FROM oidc_policies WHERE project_id = ?", src.ID); err != nil {
		return err
	}
	return nil
}

func (d *DB) countRows(ctx context.Context, query string, arg any) (int, error) {
	var n int
	if err := d.QueryRowContext(ctx, query, arg).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func (d *DB) versionTaken(ctx context.Context, projectID int64, version string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM releases WHERE project_id = ? AND version = ?", projectID, version).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("version probe: %w", err)
	}
	return n > 0, nil
}

// ApplyProjectMerge executes a plan atomically, refusing when the database no
// longer matches what the operator saw.
func (d *DB) ApplyProjectMerge(ctx context.Context, confirmed *ProjectMergePlan) error {
	if !confirmed.Applicable() {
		return fmt.Errorf("merge plan has %d unresolved conflict(s)", len(confirmed.Conflicts))
	}
	fresh, err := d.PlanProjectMerge(ctx, confirmed.From.Name, confirmed.Into.Name)
	if err != nil {
		return err
	}
	if err := samePlan(confirmed, fresh); err != nil {
		return fmt.Errorf("database changed since the dry run: %w", err)
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin merge: %w", err)
	}
	defer tx.Rollback()
	q := d.q.WithTx(tx)

	src, dst := fresh.From, fresh.Into
	// Descending: an ascending pass collides with a row it has not moved yet.
	for i := len(fresh.Releases) - 1; i >= 0; i-- {
		r := fresh.Releases[i]
		if err := q.MoveReleaseToProject(ctx, MoveReleaseToProjectParams{
			ProjectID:  dst.ID,
			Version:    r.NewVersion,
			VersionNum: r.NewNum,
			ID:         r.ID,
		}); err != nil {
			return fmt.Errorf("move release %d (%s -> %s): %w", r.ID, r.OldVersion, r.NewVersion, err)
		}
	}

	for _, key := range fresh.DupBlobs {
		if err := q.DeleteOCIBlobLink(ctx, DeleteOCIBlobLinkParams{ProjectID: src.ID, StorageKey: key}); err != nil {
			return fmt.Errorf("drop duplicate blob link %s: %w", key, err)
		}
	}

	reassign := []struct {
		what string
		fn   func(context.Context, int64, int64) error
	}{
		{"sites", func(c context.Context, to, from int64) error {
			return q.ReassignSites(c, ReassignSitesParams{ProjectID: to, ProjectID_2: from})
		}},
		{"oci tags", func(c context.Context, to, from int64) error {
			return q.ReassignOCITags(c, ReassignOCITagsParams{ProjectID: to, ProjectID_2: from})
		}},
		{"oci blob links", func(c context.Context, to, from int64) error {
			return q.ReassignOCIBlobLinks(c, ReassignOCIBlobLinksParams{ProjectID: to, ProjectID_2: from})
		}},
		{"api tokens", func(c context.Context, to, from int64) error {
			return q.ReassignAPITokens(c, ReassignAPITokensParams{ProjectID: &to, ProjectID_2: &from})
		}},
		{"oidc policies", func(c context.Context, to, from int64) error {
			return q.ReassignOIDCPolicies(c, ReassignOIDCPoliciesParams{ProjectID: &to, ProjectID_2: &from})
		}},
		{"aliases", func(c context.Context, to, from int64) error {
			return q.ReassignProjectAliases(c, ReassignProjectAliasesParams{ProjectID: to, ProjectID_2: from})
		}},
	}
	for _, step := range reassign {
		if err := step.fn(ctx, dst.ID, src.ID); err != nil {
			return fmt.Errorf("reassign %s: %w", step.what, err)
		}
	}

	// Keeps every dl URL, apt package and brew formula naming the source alive.
	if err := q.InsertProjectAlias(ctx, InsertProjectAliasParams{Name: src.Name, ProjectID: dst.ID}); err != nil {
		return fmt.Errorf("alias %q -> %q: %w", src.Name, dst.Name, err)
	}
	if err := q.DeleteProject(ctx, src.ID); err != nil {
		return fmt.Errorf("delete merged project %q: %w", src.Name, err)
	}

	if err := assertProjectDrained(ctx, tx, src.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit merge: %w", err)
	}
	return nil
}

// assertProjectDrained fails the transaction if anything still points at the
// merged-away project, so a table added later cannot leave orphans behind.
func assertProjectDrained(ctx context.Context, tx *sql.Tx, projectID int64) error {
	for _, table := range []string{"releases", "api_tokens", "oidc_policies", "sites", "oci_blob_links", "oci_tags", "project_aliases"} {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE project_id = ?", projectID).Scan(&n); err != nil {
			return fmt.Errorf("drain check %s: %w", table, err)
		}
		if n != 0 {
			return fmt.Errorf("drain check: %d row(s) still reference the merged project in %s", n, table)
		}
	}
	return nil
}

func samePlan(a, b *ProjectMergePlan) error {
	if a.From.ID != b.From.ID || a.Into.ID != b.Into.ID {
		return errors.New("project ids changed")
	}
	if len(a.Releases) != len(b.Releases) {
		return fmt.Errorf("release count changed: %d -> %d", len(a.Releases), len(b.Releases))
	}
	for i := range a.Releases {
		if a.Releases[i] != b.Releases[i] {
			return fmt.Errorf("release %d changed", a.Releases[i].ID)
		}
	}
	if len(b.Conflicts) != 0 {
		return fmt.Errorf("%d new conflict(s)", len(b.Conflicts))
	}
	return nil
}

// SnapshotTo writes a consistent copy of the live database to path, usable as a
// backup of a running server.
func (d *DB) SnapshotTo(ctx context.Context, path string) error {
	if _, err := d.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("snapshot database to %s: %w", path, err)
	}
	return nil
}
