package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/buildinfo"
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("buildhost %s (%s)\n", buildinfo.Version(), buildinfo.Commit())
	},
}
