package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
)

func init() {
	mergeCmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge a project into another project of the same GitHub repo",
		Long: "Folds a project left behind by a GitHub rename into the project that now carries " +
			"the repo's name. Releases move and are renumbered onto the target, sites, OCI tags, " +
			"tokens and policies are repointed, and the merged name is kept as a permanent alias " +
			"so published URLs keep resolving.\n\n" +
			"Writes a full database snapshot first, then applies every change in one transaction. " +
			"Pass --dry-run to print the plan without touching anything.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			from, _ := cmd.Flags().GetString("from")
			into, _ := cmd.Flags().GetString("into")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			snapshotDir, _ := cmd.Flags().GetString("snapshot-dir")
			if from == "" || into == "" {
				return fmt.Errorf("--from and --into are both required")
			}

			cfg := config.Load()
			database, err := db.Open(cfg.DBPath)
			if err != nil {
				return fmt.Errorf("open database: %w", err)
			}
			defer database.Close()

			plan, err := database.PlanProjectMerge(cmd.Context(), from, into)
			if err != nil {
				return err
			}
			printMergePlan(plan, !dryRun)
			if !plan.Applicable() {
				return fmt.Errorf("merge refused: %d unresolved conflict(s)", len(plan.Conflicts))
			}
			if dryRun {
				fmt.Println("\nDRY RUN -- nothing changed.")
				return nil
			}

			// The snapshot is not optional: this database has no other backup,
			// and a merge rewrites history in place instead of appending.
			if snapshotDir == "" {
				snapshotDir = filepath.Dir(cfg.DBPath)
			}
			snap := filepath.Join(snapshotDir, fmt.Sprintf("buildhost-premerge-%s.db", time.Now().UTC().Format("20060102T150405Z")))
			if err := database.SnapshotTo(cmd.Context(), snap); err != nil {
				return fmt.Errorf("refusing to merge without a snapshot: %w", err)
			}
			fmt.Printf("\nsnapshot written: %s\n", snap)

			if err := database.ApplyProjectMerge(cmd.Context(), plan); err != nil {
				return fmt.Errorf("merge failed (no changes committed; snapshot at %s): %w", snap, err)
			}
			fmt.Printf("merged %q into %q\n", plan.From.Name, plan.Into.Name)
			fmt.Printf("  %q now resolves as an alias of %q\n", plan.From.Name, plan.Into.Name)
			return nil
		},
	}
	mergeCmd.Flags().String("from", "", "Project to merge away (kept as an alias)")
	mergeCmd.Flags().String("into", "", "Project that survives")
	mergeCmd.Flags().Bool("dry-run", false, "Print the plan without changing anything")
	mergeCmd.Flags().String("snapshot-dir", "", "Where to write the pre-merge snapshot (default: alongside the database)")
	projectCmd.AddCommand(mergeCmd)
}

func printMergePlan(p *db.ProjectMergePlan, applying bool) {
	mode := "DRY RUN"
	if applying {
		mode = "APPLYING"
	}
	fmt.Printf("buildhost project merge -- %s\n", mode)
	fmt.Printf("  from: %-40s (id %d, repo %s, repo_id %s)\n", p.From.Name, p.From.ID, p.From.GithubRepo, p.From.GithubRepoID)
	fmt.Printf("  into: %-40s (id %d, repo %s, repo_id %s)\n", p.Into.Name, p.Into.ID, p.Into.GithubRepo, p.Into.GithubRepoID)

	fmt.Printf("  releases moved: %d\n", len(p.Releases))
	for _, r := range p.Releases {
		fmt.Printf("    release %-6d %s -> %s\n", r.ID, r.OldVersion, r.NewVersion)
	}
	fmt.Printf("  sites: %d   oci tags: %d   oci blob links: %d (%d duplicates dropped)\n",
		p.Sites, p.OCITags, p.OCIBlobs, len(p.DupBlobs))
	fmt.Printf("  api tokens: %d   oidc policies: %d\n", p.Tokens, p.Policies)
	fmt.Printf("  names kept as aliases of %q: %v\n", p.Into.Name, p.Aliases)

	for _, c := range p.Conflicts {
		fmt.Printf("  CONFLICT: %s\n", c)
	}
}
