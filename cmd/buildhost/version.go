package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var (
	buildVersion string
	buildCommit  string
	buildDate    string
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				buildCommit = s.Value
			case "vcs.time":
				buildDate = s.Value
			}
		}
		if buildVersion == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			buildVersion = info.Main.Version
		}
	}
	if buildVersion == "" {
		buildVersion = "dev"
	}
	if buildCommit == "" {
		buildCommit = "none"
	}

	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("buildhost %s (%s)\n", buildVersion, buildCommit)
	},
}
