package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/auth"
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
		settings, err := database.GetRetentionSettings(cmd.Context())
		if err != nil {
			return fmt.Errorf("load retention settings: %w", err)
		}
		ret := retention.New(database, store, retention.ConfigFromSettings(settings, enforce || cfg.RetentionEnforce)).
			WithRecordDeleter(recordDeleterFor(cfg))

		rep, err := ret.Run(cmd.Context())
		if err != nil {
			return fmt.Errorf("gc: %w", err)
		}
		printGCReport(rep, settings)
		if rep.RecordsUnmarked > 0 && rep.Enforced {
			return fmt.Errorf("%d artifact storage record(s) could not be marked deleted; the org's linked artifacts page still lists evicted artifacts as stored", rep.RecordsUnmarked)
		}
		return nil
	},
}

// recordDeleterFor builds the sink that retracts an evicted release's
// artifact-metadata storage records, authenticating as buildhost itself.
//
// RegistryURL must match the registry_url the publishing CI recorded, which is
// buildhost's own public base URL. The server is otherwise never told its own
// URL (every generated link is derived from the request Host), but a background
// sweep has no request to derive one from -- so this is the one place it needs
// configuring, and BUILDHOST_PRIMARY_DOMAIN already names that apex. When it is
// unset the deleter still runs and fails loudly per record rather than
// silently leaving the org's page stale.
func recordDeleterFor(cfg config.Config) retention.RecordDeleter {
	registry := ""
	if cfg.PrimaryDomain != "" {
		registry = "https://" + cfg.PrimaryDomain
	}
	return &retention.GitHubRecordDeleter{RegistryURL: registry, Bearer: auth.BearerForRepo}
}

func printGCReport(rep retention.Report, settings db.RetentionSettings) {
	if rep.Enforced {
		fmt.Println("buildhost gc -- ENFORCING (deletions applied)")
	} else {
		fmt.Println("buildhost gc -- DRY RUN (nothing deleted; pass --enforce to apply)")
	}
	fmt.Printf("  keep-N per (project, branch): %d   recency guard: %dh\n", settings.KeepN, settings.RecencyHours)
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

	// An evicted artifact whose storage record still says "stored" is a lie on
	// the org's linked artifacts page, so the numbers are always printed --
	// including the zero case, so a run that retracted nothing cannot be read
	// as a run that had nothing to retract.
	if rep.Enforced {
		fmt.Printf("  storage records marked deleted: %d (%d could not be marked)\n", rep.RecordsMarkedDeleted, rep.RecordsUnmarked)
	} else {
		fmt.Printf("  storage records that would be marked deleted: %d\n", rep.RecordsUnmarked)
	}
	for _, e := range rep.RecordErrors {
		fmt.Printf("    record error: %s\n", e)
	}
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
