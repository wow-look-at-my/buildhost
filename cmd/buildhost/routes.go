package main

import (
	"fmt"
	"slices"

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
		p := auth.Patterns()
		slices.Sort(p)
		for _, pat := range p {
			fmt.Println(pat)
		}
	},
}
