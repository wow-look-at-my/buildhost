package main

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "buildhost",
	Short: "Universal package registry server",
}
