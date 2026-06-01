package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/auth"
	"github.com/wow-look-at-my/buildhost/internal/config"
)

func init() {
	rootCmd.AddCommand(routesCmd)
}

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Print all registered HTTP routes",
	Run: func(_ *cobra.Command, _ []string) {
		cfg := config.Load()
		auth.Init(nil, nil, cfg.BaseURL, cfg.DataDir, cfg.OIDCIssuers, cfg.OIDCOrgs, cfg.OIDCEvents, cfg.SiteFetchDomains)
		for _, r := range auth.AllRoutes() {
			fmt.Println(r)
		}
	},
}
