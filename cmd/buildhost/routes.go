package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/auth"
)

func init() {
	rootCmd.AddCommand(routesCmd)
}

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Print all registered HTTP routes",
	Run: func(_ *cobra.Command, _ []string) {
		for _, r := range auth.AllRoutes() {
			fmt.Println(r)
		}
	},
}
