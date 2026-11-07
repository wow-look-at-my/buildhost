package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/wow-look-at-my/buildhost/internal/runarchive"
)

func init() {
	archiveCmd.AddCommand(archivePackCmd, archiveUnpackCmd)
	rootCmd.AddCommand(archiveCmd)
}

var archiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Pack or unpack the zstd tar the hand-off and cache actions send",
}

var archivePackCmd = &cobra.Command{
	Use:   "pack DIR ARCHIVE [MEMBER...]",
	Short: "Write members of DIR, by default all of it, to ARCHIVE",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		out, err := os.Create(args[1])
		if err != nil {
			return err
		}
		if err := runarchive.Pack(out, args[0], args[2:]); err != nil {
			out.Close()
			return fmt.Errorf("pack %s: %w", args[0], err)
		}
		return out.Close()
	},
}

var archiveUnpackCmd = &cobra.Command{
	Use:   "unpack ARCHIVE DIR",
	Short: "Extract ARCHIVE into DIR",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		in, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer in.Close()
		if err := runarchive.Unpack(in, args[1]); err != nil {
			return fmt.Errorf("unpack into %s: %w", args[1], err)
		}
		return nil
	},
}
