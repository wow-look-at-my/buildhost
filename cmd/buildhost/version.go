package main

import (
	"fmt"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
)

var buildVersion = "dev"

func init() {
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("buildhost %s (%s)\n", resolvedVersion(), resolvedCommit())
	},
}

func resolvedVersion() string {
	if buildVersion != "dev" {
		return buildVersion
	}
	if vcs := getVCS(); vcs.time != "" {
		if t, err := time.Parse(time.RFC3339, vcs.time); err == nil {
			return fmt.Sprintf("v0.0.%d", t.Unix())
		}
	}
	return buildVersion
}

func resolvedCommit() string {
	if vcs := getVCS(); vcs.revision != "" {
		return vcs.revision
	}
	return "unknown"
}

func resolvedDate() string {
	if vcs := getVCS(); vcs.time != "" {
		return vcs.time
	}
	return ""
}

type vcsData struct {
	revision string
	time     string
	modified bool
}

var cachedVCS *vcsData

func getVCS() vcsData {
	if cachedVCS != nil {
		return *cachedVCS
	}
	var v vcsData
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				v.revision = s.Value
			case "vcs.time":
				v.time = s.Value
			case "vcs.modified":
				v.modified = s.Value == "true"
			}
		}
	}
	cachedVCS = &v
	return v
}
