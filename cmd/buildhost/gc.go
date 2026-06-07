package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/config"
	"github.com/wow-look-at-my/buildhost/internal/db"
	"github.com/wow-look-at-my/buildhost/internal/retention"
	"github.com/wow-look-at-my/buildhost/internal/storage"
)

func init() {
	rootCmd.AddCommand(gcCmd)
	gcCmd.Flags().Bool("enforce", false, "actually delete (default: report-only dry run)")
}

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage-collect evictable releases and unreferenced blobs",
	Long: "Evicts published releases past keep-N on each (project, branch) and abandoned " +
		"unpublished uploads, then deletes any content-addressed blob no longer referenced " +
		"by anything. Report-only by default: pass --enforce (or set " +
		"BUILDHOST_RETENTION_ENFORCE=true) to actually delete.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg := config.Load()

		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer database.Close()

		fsStore, err := storage.NewFilesystem(cfg.DataDir+"/blobs", cfg.StorageCompress)
		if err != nil {
			return fmt.Errorf("init storage: %w", err)
		}
		store := storage.NewTraced(fsStore)

		enforce, _ := cmd.Flags().GetBool("enforce")
		ret := retention.New(database, store, retention.Config{
			KeepN:        cfg.RetentionKeepN,
			RecencyGuard: cfg.RetentionRecencyGuard,
			Enforce:      enforce || cfg.RetentionEnforce,
		})

		rep, err := ret.Run(cmd.Context())
		if err != nil {
			return fmt.Errorf("gc: %w", err)
		}
		printGCReport(rep, cfg)
		return nil
	},
}

func printGCReport(rep retention.Report, cfg config.Config) {
	if rep.Enforced {
		fmt.Println("buildhost gc -- ENFORCING (deletions applied)")
	} else {
		fmt.Println("buildhost gc -- DRY RUN (nothing deleted; pass --enforce to apply)")
	}
	fmt.Printf("  keep-N per (project, branch): %d   recency guard: %s\n", cfg.RetentionKeepN, cfg.RetentionRecencyGuard)
	fmt.Printf("  releases: %d (%d past keep-N, %d abandoned)\n", rep.Releases(), len(rep.EvictedReleases), len(rep.AbandonedReleases))

	for _, r := range rep.EvictedReleases {
		fmt.Printf("    keep-N   project=%d branch=%s %s (release %d)\n", r.ProjectID, branchLabel(r.Branch), r.Version, r.ID)
	}
	for _, r := range rep.AbandonedReleases {
		fmt.Printf("    abandon  project=%d branch=%s %s (release %d)\n", r.ProjectID, branchLabel(r.Branch), r.Version, r.ID)
	}

	verb := "would free"
	if rep.Enforced {
		verb = "freed"
	}
	fmt.Printf("  blobs %s: %d (%s); %d shared blobs kept\n", verb, rep.BlobsDeleted, humanBytes(rep.ReclaimableBytes), rep.BlobsRetained)
}

func branchLabel(b string) string {
	if b == "" {
		return "(none)"
	}
	return b
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
